package analysis

import (
	"cmp"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/trace-analyzer/internal/models"
)

// BaselineComputer tracks canonical topologies and performance baselines per service
// Uses EWMA (Exponentially Weighted Moving Average) for adaptive baselines per industry best practices
type BaselineComputer struct {
	services                 map[string]*ServiceBaseline
	mu                       sync.RWMutex
	minSamples               int
	baselineAlpha            float64         // EWMA decay factor (0.0-1.0), higher = more weight to recent data
	variant                  BaselineVariant // Which variant strategy this computer uses
	slidingWindowDuration    time.Duration   // For sliding window variant
	exponentialDecayHalfLife time.Duration   // For exponential decay variant
	fingerprinter            *Fingerprinter  // Reused fingerprinter instance
	hourly                   *HourlyHistory  // same_time_yesterday only
	sharedHourly             bool            // hourly is fed by VariantManager.UpdateAll, not update
	now                      time.Time       // latest trace StartTime learned, see observe
}

// MaxFutureSkew is how far a trace's StartTime may run ahead of the wall clock
// and still advance trace time. A trace further ahead is learned, but it moves
// neither the new-path window, the recent window nor hourly retention.
const MaxFutureSkew = 5 * time.Minute

// nowFunc is the wall clock. Only tooFarAhead and observe read it.
var nowFunc = time.Now

func futureLimit() time.Time { return nowFunc().Add(MaxFutureSkew) }

func tooFarAhead(t time.Time) bool { return t.After(futureLimit()) }

// ServiceBaseline holds baseline data for a service identity
type ServiceBaseline struct {
	ServiceID            string
	ServiceGrouping      models.ServiceGrouping
	TopologyCounts       map[string]int64         // fingerprint -> count
	TopologyExamples     map[string]*models.Trace // fingerprint -> sample trace
	TopologyFirstSeen    map[string]time.Time     // fingerprint -> first seen time
	CanonicalFingerprint string
	KnownGoodPaths       map[string]bool // paths with >1% frequency
	TotalTraces          int64
	ErrorCount           int64

	// EWMA-based adaptive frequency tracking (replaces pure cumulative)
	TopologyFrequenciesEWMA map[string]float64 // fingerprint -> EWMA frequency (0.0-1.0)
	LastUpdateTime          time.Time          // For time-weighted updates

	// Frequency shift detection
	RecentWindow        map[string]int64 // last 5-min window counts
	RecentWindowStart   time.Time
	PreviousFrequencies map[string]float64 // frequencies from last window

	// Branch-level baselines
	BranchBaselines map[string]*BranchBaseline // branch_key -> baseline

	// Per-path duration baselines (each topology has its own expected duration)
	PathBaselines map[string]*PathBaseline // fingerprint -> duration baseline

	// Overall duration stats
	DurationSum     int64
	DurationMin     int64
	DurationMax     int64
	DurationSamples []int64 // For percentile calculation (limited size)

	LastSeen time.Time

	// fingerprint -> last trace time, for sliding_window and exponential_decay
	topologyLastSeen map[string]time.Time
	// imported paths with no trustworthy last-seen; the next trace stamps them
	lastSeenPending []string
}

func isGroupingEmpty(grouping models.ServiceGrouping) bool {
	return grouping.ServiceIdentity == "" &&
		grouping.Scope1 == "" &&
		grouping.Scope2 == "" &&
		grouping.Scope3 == "" &&
		grouping.Env == "" &&
		grouping.Operation == "" &&
		grouping.FeatureGroup == "" &&
		grouping.FeatureName == "" &&
		grouping.SubService == ""
}

// BranchBaseline holds statistics for a single branch
type BranchBaseline struct {
	Count       int64
	SumDuration int64
	MinDuration int64
	MaxDuration int64
	ErrorCount  int64
	Samples     []int64 // Recent samples for percentile estimation
	mu          sync.Mutex
}

// PathBaseline holds duration stats per (service, fingerprint) combo
type PathBaseline struct {
	Count          int64
	SumDuration    int64
	MinDuration    int64
	MaxDuration    int64
	Samples        []int64
	LastUpdateTime time.Time // For time-based variants (sliding window, exponential decay)
	mu             sync.Mutex
	// weights[i] is how many traces Samples[i] stands for, set when merged
	// hourly reservoirs sample their hours at different rates; nil means 1 each.
	weights []float64
}

// NewBaselineComputer creates a new baseline computer with EWMA-based adaptive baselines
// alpha: EWMA decay factor (0.0-1.0). Default 0.1 means recent data gets ~10% weight per update
// Higher alpha (e.g., 0.3) adapts faster but less stable. Lower alpha (e.g., 0.05) more stable but slower adaptation.
func NewBaselineComputer(minSamples int) *BaselineComputer {
	return &BaselineComputer{
		services:                 make(map[string]*ServiceBaseline),
		minSamples:               minSamples,
		baselineAlpha:            0.1, // Default: recent data gets 10% weight, 90% from history (balanced adaptation)
		variant:                  VariantCumulative,
		slidingWindowDuration:    1 * time.Hour,
		exponentialDecayHalfLife: 24 * time.Hour,
		fingerprinter:            NewFingerprinter(), // Reuse fingerprinter instance
	}
}

// SetVariant sets the baseline computation variant
func (bc *BaselineComputer) SetVariant(variant BaselineVariant) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.variant = variant
	if variant == VariantSameTimeYesterday && bc.hourly == nil {
		bc.hourly = NewHourlyHistory(DefaultHourlyPathSampleCap)
	}
}

// useSharedHourly makes bc read h, which its owner feeds once per trace.
// Call before SetVariant so no private history is allocated.
func (bc *BaselineComputer) useSharedHourly(h *HourlyHistory) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.hourly = h
	bc.sharedHourly = true
}

func (bc *BaselineComputer) hourlyHistory() *HourlyHistory {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.hourly
}

// SetSlidingWindowDuration sets the window duration for sliding window variant
func (bc *BaselineComputer) SetSlidingWindowDuration(duration time.Duration) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.slidingWindowDuration = duration
}

// SetExponentialDecayHalfLife sets the half-life for exponential decay variant
func (bc *BaselineComputer) SetExponentialDecayHalfLife(halfLife time.Duration) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.exponentialDecayHalfLife = halfLife
}

// SetBaselineAlpha configures the EWMA decay factor (0.0-1.0)
// 0.05 = very stable, slow adaptation (weeks)
// 0.1 = balanced (days) - DEFAULT
// 0.3 = fast adaptation (hours)
func (bc *BaselineComputer) SetBaselineAlpha(alpha float64) {
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 1 {
		alpha = 1
	}
	bc.mu.Lock()
	defer bc.mu.Unlock()
	bc.baselineAlpha = alpha
}

// update takes branches precomputed so one extraction can feed every combination.
func (bc *BaselineComputer) update(trace *models.Trace, fingerprint string, branches []Branch) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	serviceID := trace.ServiceID
	stamp := bc.observe(trace.StartTime)

	baseline, exists := bc.services[serviceID]
	if !exists {
		baseline = &ServiceBaseline{
			ServiceID:               serviceID,
			TopologyCounts:          make(map[string]int64),
			TopologyExamples:        make(map[string]*models.Trace),
			TopologyFirstSeen:       make(map[string]time.Time),
			KnownGoodPaths:          make(map[string]bool),
			BranchBaselines:         make(map[string]*BranchBaseline),
			PathBaselines:           make(map[string]*PathBaseline),
			DurationMin:             math.MaxInt64,
			RecentWindow:            make(map[string]int64),
			RecentWindowStart:       bc.now,
			PreviousFrequencies:     make(map[string]float64),
			TopologyFrequenciesEWMA: make(map[string]float64),
			LastUpdateTime:          time.Now(),
		}
		bc.services[serviceID] = baseline
	}

	if isGroupingEmpty(baseline.ServiceGrouping) && !isGroupingEmpty(trace.ServiceGrouping) {
		baseline.ServiceGrouping = trace.ServiceGrouping
	}

	// Update topology counts (cumulative for historical reference)
	baseline.TopologyCounts[fingerprint]++
	baseline.TotalTraces++

	// Track first seen time for new paths
	if _, seen := baseline.TopologyFirstSeen[fingerprint]; !seen {
		baseline.TopologyFirstSeen[fingerprint] = stamp
	}

	// Store example trace for this topology
	if _, hasExample := baseline.TopologyExamples[fingerprint]; !hasExample {
		baseline.TopologyExamples[fingerprint] = trace
	}

	// Update frequency based on variant strategy
	switch bc.variant {
	case VariantEWMA:
		bc.updateEWMAFrequency(baseline, fingerprint)
	case VariantSlidingWindow:
		bc.updateSlidingWindowFrequency(baseline, fingerprint, stamp)
	case VariantExponentialDecay:
		bc.updateExponentialDecayFrequency(baseline, fingerprint, stamp)
	case VariantSameTimeYesterday:
		if !bc.sharedHourly {
			bc.hourly.Add(serviceID, fingerprint, trace.TotalDurationUs, trace.StartTime)
		}
	default: // VariantCumulative
		// Cumulative is already updated via TopologyCounts increment above
	}

	// Update recent window for frequency shift detection (used by all variants)
	bc.updateRecentWindow(baseline, fingerprint)

	// Update known good paths (>1% frequency)
	bc.updateKnownGoodPaths(baseline)

	baseline.LastUpdateTime = time.Now()

	// Update per-path duration baseline (variant-specific)
	bc.updatePathBaselineVariant(baseline, fingerprint, trace.TotalDurationUs, stamp)

	// Update duration stats
	duration := trace.TotalDurationUs
	baseline.DurationSum += duration
	if duration < baseline.DurationMin {
		baseline.DurationMin = duration
	}
	if duration > baseline.DurationMax {
		baseline.DurationMax = duration
	}

	baseline.DurationSamples = appendCapped(baseline.DurationSamples, duration, 1000, baseline.TotalTraces)

	// Update error count
	if trace.HasError {
		baseline.ErrorCount++
	}

	// Update branch baselines
	for _, branch := range branches {
		key := branch.Key()
		branchBaseline, exists := baseline.BranchBaselines[key]
		if !exists {
			branchBaseline = &BranchBaseline{
				MinDuration: math.MaxInt64,
			}
			baseline.BranchBaselines[key] = branchBaseline
		}

		branchBaseline.Count++
		branchBaseline.SumDuration += branch.DurationUs
		if branch.DurationUs < branchBaseline.MinDuration {
			branchBaseline.MinDuration = branch.DurationUs
		}
		if branch.DurationUs > branchBaseline.MaxDuration {
			branchBaseline.MaxDuration = branch.DurationUs
		}

		branchBaseline.Samples = appendCapped(branchBaseline.Samples, branch.DurationUs, 100, branchBaseline.Count)
	}

	baseline.LastSeen = time.Now()
}

// observe advances bc.now to t unless t is more than MaxFutureSkew ahead of
// the wall clock, and returns the time to stamp the trace with. Stamps never
// go behind bc.now: a late trace is stamped bc.now, so a path it introduces
// gets the full new-path window and decay ages stay non-negative. A trace too
// far ahead is stamped bc.now, or the wall clock before any trace, so it
// cannot plant a future first-seen. Caller holds bc.mu.
func (bc *BaselineComputer) observe(t time.Time) time.Time {
	if !t.After(bc.now) {
		return bc.now
	}
	if tooFarAhead(t) {
		if bc.now.IsZero() {
			return nowFunc()
		}
		return bc.now
	}
	bc.now = t
	return t
}

func (bc *BaselineComputer) updateKnownGoodPaths(baseline *ServiceBaseline) {
	if baseline.TotalTraces < int64(bc.minSamples) {
		return
	}

	// Update known good paths (>1% frequency)
	for fp, count := range baseline.TopologyCounts {
		freq := float64(count) / float64(baseline.TotalTraces)
		if freq > 0.01 { // >1% is "known good"
			baseline.KnownGoodPaths[fp] = true
		}
	}

	// Find most common topology for canonical (still useful for UI)
	var maxCount int64
	var maxFingerprint string
	for fp, count := range baseline.TopologyCounts {
		if count > maxCount {
			maxCount = count
			maxFingerprint = fp
		}
	}

	if float64(maxCount)/float64(baseline.TotalTraces) > 0.1 {
		baseline.CanonicalFingerprint = maxFingerprint
	}
}

const (
	recentWindowDuration = 5 * time.Minute
	newPathWindow        = 10 * time.Minute
)

func (bc *BaselineComputer) updateRecentWindow(baseline *ServiceBaseline, fingerprint string) {
	now := bc.now

	// Check if we need to rotate the window
	if now.Sub(baseline.RecentWindowStart) > recentWindowDuration {
		// Save current frequencies before rotating
		total := int64(0)
		for _, count := range baseline.RecentWindow {
			total += count
		}
		if total > 0 {
			baseline.PreviousFrequencies = make(map[string]float64)
			for fp, count := range baseline.RecentWindow {
				baseline.PreviousFrequencies[fp] = float64(count) / float64(total)
			}
		}

		// Reset window
		baseline.RecentWindow = make(map[string]int64)
		baseline.RecentWindowStart = now
	}

	baseline.RecentWindow[fingerprint]++
}

// IsKnownGoodPath returns true if this path has >1% historical frequency
func (bc *BaselineComputer) IsKnownGoodPath(serviceID, fingerprint string) bool {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	baseline, exists := bc.services[serviceID]
	if !exists {
		return false
	}
	return baseline.KnownGoodPaths[fingerprint]
}

// IsNewPath reports whether the path was first seen less than 10 minutes of
// trace time before the later of at and the latest trace learned. at is the
// StartTime of the trace being checked, which is not learned yet.
func (bc *BaselineComputer) IsNewPath(serviceID, fingerprint string, at time.Time) bool {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	baseline, exists := bc.services[serviceID]
	if !exists {
		return true
	}

	firstSeen, exists := baseline.TopologyFirstSeen[fingerprint]
	if !exists {
		return true
	}

	ref := bc.now
	if at.After(ref) && !tooFarAhead(at) {
		ref = at
	}
	return ref.Sub(firstSeen) < newPathWindow
}

// GetPathFrequency returns the historical frequency of a path
func (bc *BaselineComputer) GetPathFrequency(serviceID, fingerprint string) float64 {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	baseline, exists := bc.services[serviceID]
	if !exists || baseline.TotalTraces == 0 {
		return 0
	}

	count := baseline.TopologyCounts[fingerprint]
	return float64(count) / float64(baseline.TotalTraces)
}

// DetectFrequencyShift checks if a path's frequency changed significantly.
// It reads the windows as of the last learned trace: the window rotates when
// a trace is learned, not when one is checked, so a check never sees an
// empty window just because its own trace starts a new one.
func (bc *BaselineComputer) DetectFrequencyShift(serviceID, fingerprint string) (shifted bool, oldFreq, newFreq float64) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	baseline, exists := bc.services[serviceID]
	if !exists {
		return false, 0, 0
	}

	oldFreq = baseline.PreviousFrequencies[fingerprint]

	// Calculate current window frequency
	total := int64(0)
	for _, count := range baseline.RecentWindow {
		total += count
	}
	if total < 20 { // Need enough samples
		return false, oldFreq, 0
	}

	newFreq = float64(baseline.RecentWindow[fingerprint]) / float64(total)

	// Significant shift: >10% absolute change or >50% relative change
	absDiff := math.Abs(newFreq - oldFreq)
	if oldFreq > 0 {
		relDiff := absDiff / oldFreq
		if absDiff > 0.1 || relDiff > 0.5 {
			return true, oldFreq, newFreq
		}
	} else if newFreq > 0.1 { // New path appeared with >10% frequency
		return true, oldFreq, newFreq
	}

	return false, oldFreq, newFreq
}

// Percentiles calculates multiple percentiles from one sort (thread-safe)
func (p *PathBaseline) Percentiles(pcts ...float64) []float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.weights != nil {
		return weightedPercentiles(p.Samples, p.weights, pcts...)
	}
	return sortedPercentiles(p.Samples, pcts...)
}

// weightedPercentiles is sortedPercentiles over a population where samples[i]
// stands for weights[i] traces: for each pct it returns the smallest sample
// whose cumulative weight passes rank (W-1)*pct of the W weighted traces. With
// every weight 1 that is samples[int((n-1)*pct)], the same as sortedPercentiles.
func weightedPercentiles(samples []int64, weights []float64, pcts ...float64) []float64 {
	results := make([]float64, len(pcts))
	if len(samples) == 0 {
		return results
	}
	order := make([]int, len(samples))
	var total float64
	for i := range order {
		order[i] = i
		total += weights[i]
	}
	slices.SortFunc(order, func(a, b int) int { return cmp.Compare(samples[a], samples[b]) })
	for i, pct := range pcts {
		target := (total - 1) * max(0, min(1, pct))
		results[i] = float64(samples[order[len(order)-1]])
		var cum float64
		for _, j := range order {
			if cum += weights[j]; cum > target {
				results[i] = float64(samples[j])
				break
			}
		}
	}
	return results
}

var percentileScratch = sync.Pool{New: func() any { return new([]int64) }}

// sortedPercentiles returns samples[int((n-1)*pct)] of the sorted samples for
// each pct clamped to [0, 1], or zeros when there are no samples. samples is
// not modified.
func sortedPercentiles(samples []int64, pcts ...float64) []float64 {
	results := make([]float64, len(pcts))
	if len(samples) == 0 {
		return results
	}
	buf := percentileScratch.Get().(*[]int64)
	sorted := append((*buf)[:0], samples...)
	slices.Sort(sorted)
	for i, pct := range pcts {
		pct = max(0, min(1, pct))
		results[i] = float64(sorted[int(float64(len(sorted)-1)*pct)])
	}
	*buf = sorted
	percentileScratch.Put(buf)
	return results
}

// appendCapped keeps at most limit samples. It grows the slice on demand so
// rarely seen paths stay small, and once full overwrites slot ringIdx%limit.
func appendCapped(s []int64, v int64, limit int, ringIdx int64) []int64 {
	if len(s) >= limit {
		s[int(ringIdx%int64(limit))] = v
		return s
	}
	if len(s) == cap(s) {
		grown := make([]int64, len(s), min(max(8, 2*cap(s)), limit))
		copy(grown, s)
		s = grown
	}
	return append(s, v)
}

// withService runs f on the live baseline under the read lock and reports
// whether the service exists. f must not call other BaselineComputer methods.
func (bc *BaselineComputer) withService(serviceID string, f func(*ServiceBaseline)) bool {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	b, ok := bc.services[serviceID]
	if ok {
		f(b)
	}
	return ok
}

// ServiceSummary is a copy of a service baseline's headline stats.
type ServiceSummary struct {
	ServiceID            string
	ServiceGrouping      models.ServiceGrouping
	TotalTraces          int64
	ErrorCount           int64
	TopologyCount        int
	CanonicalFingerprint string
	DurationSum          int64
	DurationMin          int64
	DurationMax          int64
	SampleCount          int
	SampleMean           float64
	P50                  float64
	P75                  float64
	P90                  float64
	P99                  float64
	LastSeen             time.Time
}

// GetServiceSummary returns a snapshot of the service baseline taken under the read lock.
func (bc *BaselineComputer) GetServiceSummary(serviceID string) (ServiceSummary, bool) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	b, ok := bc.services[serviceID]
	if !ok {
		return ServiceSummary{}, false
	}
	sum := ServiceSummary{
		ServiceID:            b.ServiceID,
		ServiceGrouping:      b.ServiceGrouping,
		TotalTraces:          b.TotalTraces,
		ErrorCount:           b.ErrorCount,
		TopologyCount:        len(b.TopologyCounts),
		CanonicalFingerprint: b.CanonicalFingerprint,
		DurationSum:          b.DurationSum,
		DurationMin:          b.DurationMin,
		DurationMax:          b.DurationMax,
		SampleCount:          len(b.DurationSamples),
		LastSeen:             b.LastSeen,
	}
	if len(b.DurationSamples) > 0 {
		sorted := append([]int64(nil), b.DurationSamples...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		var total int64
		for _, d := range sorted {
			total += d
		}
		sum.SampleMean = float64(total) / float64(len(sorted))
		at := func(p float64) float64 { return float64(sorted[int(float64(len(sorted)-1)*p)]) }
		sum.P50, sum.P75, sum.P90, sum.P99 = at(0.50), at(0.75), at(0.90), at(0.99)
	}
	return sum, true
}

// BranchSummary is a copy of one branch baseline's stats.
type BranchSummary struct {
	Key         string
	Count       int64
	ErrorCount  int64
	MinDuration int64
	MaxDuration int64
	Mean        float64
	P50         float64
	P90         float64
	P99         float64
}

// GetBranchSummaries returns snapshots of every branch baseline for a service.
func (bc *BaselineComputer) GetBranchSummaries(serviceID string) []BranchSummary {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	b, ok := bc.services[serviceID]
	if !ok {
		return nil
	}
	out := make([]BranchSummary, 0, len(b.BranchBaselines))
	for key, bb := range b.BranchBaselines {
		pcts := bb.Percentiles(0.5, 0.9, 0.99)
		out = append(out, BranchSummary{
			Key:         key,
			Count:       bb.Count,
			ErrorCount:  bb.ErrorCount,
			MinDuration: bb.MinDuration,
			MaxDuration: bb.MaxDuration,
			Mean:        bb.Mean(),
			P50:         pcts[0],
			P90:         pcts[1],
			P99:         pcts[2],
		})
	}
	return out
}

// GetTopologyExample returns a copy of the stored example trace for a fingerprint.
func (bc *BaselineComputer) GetTopologyExample(serviceID, fingerprint string) (*models.Trace, bool) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	b, ok := bc.services[serviceID]
	if !ok {
		return nil, false
	}
	example, ok := b.TopologyExamples[fingerprint]
	if !ok {
		return nil, false
	}
	cp := *example
	cp.Spans = append([]*models.Span(nil), example.Spans...)
	return &cp, true
}

// GetMinSamples returns the minimum samples threshold for this baseline computer
func (bc *BaselineComputer) GetMinSamples() int {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.minSamples
}

// GetVariant returns the variant type for this baseline computer
func (bc *BaselineComputer) GetVariant() BaselineVariant {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.variant
}

// GetAllServices returns all tracked services
func (bc *BaselineComputer) GetAllServices() []*models.ServiceIdentity {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	var services []*models.ServiceIdentity
	for _, baseline := range bc.services {
		avgDuration := float64(0)
		if baseline.TotalTraces > 0 {
			avgDuration = float64(baseline.DurationSum) / float64(baseline.TotalTraces)
		}

		errorRate := float64(0)
		if baseline.TotalTraces > 0 {
			errorRate = float64(baseline.ErrorCount) / float64(baseline.TotalTraces)
		}

		services = append(services, &models.ServiceIdentity{
			ServiceID:     baseline.ServiceID,
			TraceCount:    baseline.TotalTraces,
			TopologyCount: len(baseline.TopologyCounts),
			AvgDurationUs: avgDuration,
			ErrorRate:     errorRate,
			LastSeen:      baseline.LastSeen,
		})
	}

	return services
}

// GetTopologies returns topology distribution for a service
func (bc *BaselineComputer) GetTopologies(serviceID string) []*models.TopologyInfo {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	baseline, exists := bc.services[serviceID]
	if !exists {
		return nil
	}

	var topologies []*models.TopologyInfo
	for fp, count := range baseline.TopologyCounts {
		// Calculate average duration for this topology
		var avgDuration float64
		if example, ok := baseline.TopologyExamples[fp]; ok {
			avgDuration = float64(example.TotalDurationUs)
		}

		topologies = append(topologies, &models.TopologyInfo{
			Fingerprint:   fp,
			Count:         count,
			Percentage:    float64(count) / float64(baseline.TotalTraces) * 100,
			AvgDurationUs: avgDuration,
			IsCanonical:   fp == baseline.CanonicalFingerprint,
		})
	}

	// Sort by count descending
	sort.Slice(topologies, func(i, j int) bool {
		return topologies[i].Count > topologies[j].Count
	})

	return topologies
}

// GetCanonicalFingerprint returns the canonical topology for a service
func (bc *BaselineComputer) GetCanonicalFingerprint(serviceID string) string {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	baseline, exists := bc.services[serviceID]
	if !exists {
		return ""
	}
	return baseline.CanonicalFingerprint
}

// Percentiles calculates multiple percentiles from one sort (thread-safe)
func (b *BranchBaseline) Percentiles(pcts ...float64) []float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return sortedPercentiles(b.Samples, pcts...)
}

// Mean returns average duration
func (b *BranchBaseline) Mean() float64 {
	if b.Count == 0 {
		return 0
	}
	return float64(b.SumDuration) / float64(b.Count)
}

// updateEWMAFrequency updates frequency using O(1) Exponential Weighted Moving Average
// Previous implementation iterated all fingerprints O(f), now only updates current fingerprint
func (bc *BaselineComputer) updateEWMAFrequency(baseline *ServiceBaseline, fingerprint string) {
	// Get or create frequency entry for this fingerprint
	prevFreq := float64(0)
	if existing, ok := baseline.TopologyFrequenciesEWMA[fingerprint]; ok {
		prevFreq = existing
	}

	// EWMA update: new_freq = alpha * 1.0 + (1 - alpha) * previous
	// Where 1.0 represents "this fingerprint appeared"
	newFreq := bc.baselineAlpha + (1-bc.baselineAlpha)*prevFreq
	baseline.TopologyFrequenciesEWMA[fingerprint] = newFreq

	// Note: We no longer iterate all fingerprints to decay them.
	// Instead, decay is calculated lazily when frequencies are read.
	// This changes O(f) per update to O(1) per update.
}

// SlidingWindowTrace tracks a trace within a sliding window
type SlidingWindowTrace struct {
	Fingerprint string
	Timestamp   time.Time
	Duration    int64
}

// updateSlidingWindowFrequency updates frequency using a fixed time window
func (bc *BaselineComputer) updateSlidingWindowFrequency(baseline *ServiceBaseline, fingerprint string, traceTime time.Time) {
	// For sliding window, we use time-based decay to approximate a true sliding window
	// Since we don't store full trace history, we:
	// 1. Track last update time per fingerprint
	// 2. Decay counts based on time elapsed since last update
	// 3. Increment current fingerprint count

	now := traceTime
	cutoff := now.Add(-bc.slidingWindowDuration)
	lastSeenAt := baseline.lastSeenMap(now)

	// Decay all existing fingerprints based on time elapsed
	// If a fingerprint hasn't been seen within the window, its count decays to near zero
	for fp, lastSeen := range lastSeenAt {
		if fp == fingerprint {
			// Current fingerprint - will be updated below
			continue
		}

		age := now.Sub(lastSeen)
		if age > bc.slidingWindowDuration {
			// Completely outside window - remove or set to minimal value
			if baseline.TopologyCounts[fp] > 0 {
				baseline.TopologyCounts[fp] = 0
			}
		} else {
			// Within window but aging - apply decay proportional to age
			// Older traces get more decay
			ageRatio := float64(age) / float64(bc.slidingWindowDuration)
			decayFactor := 1.0 - (ageRatio * 0.1) // Decay up to 10% per window duration
			if baseline.TopologyCounts[fp] > 1 {
				baseline.TopologyCounts[fp] = int64(float64(baseline.TopologyCounts[fp]) * decayFactor)
			}
		}
	}

	// Update current fingerprint
	baseline.TopologyCounts[fingerprint]++
	lastSeenAt[fingerprint] = now

	// Periodically clean up zero counts and very old entries
	if baseline.TotalTraces%100 == 0 {
		for fp, count := range baseline.TopologyCounts {
			if count == 0 {
				// Check if it's been outside window for a while
				if lastSeen, exists := lastSeenAt[fp]; exists {
					if now.Sub(lastSeen) > bc.slidingWindowDuration*2 {
						delete(baseline.TopologyCounts, fp)
						delete(baseline.TopologyFirstSeen, fp)
						delete(lastSeenAt, fp)
					}
				}
			}
		}
	}

	_ = cutoff // Keep for reference
}

// updateExponentialDecayFrequency updates frequency using time-weighted exponential decay
func (bc *BaselineComputer) updateExponentialDecayFrequency(baseline *ServiceBaseline, fingerprint string, traceTime time.Time) {
	// Exponential decay: weight = exp(-lambda * age)
	// lambda = ln(2) / half_life
	lambda := math.Log(2) / bc.exponentialDecayHalfLife.Seconds()

	// For each existing fingerprint, decay its weight based on time since last update
	now := traceTime
	lastSeenAt := baseline.lastSeenMap(now)
	if _, ok := lastSeenAt[fingerprint]; !ok {
		lastSeenAt[fingerprint] = now
	}
	for fp, lastUpdate := range lastSeenAt {
		if fp == fingerprint {
			// This fingerprint is current, set weight to 1.0
			baseline.TopologyCounts[fp]++
			lastSeenAt[fp] = now
		} else {
			// Decay weight based on age
			age := now.Sub(lastUpdate).Seconds()
			decayWeight := math.Exp(-lambda * age)
			// Apply decay (approximate - we're using counts as weights)
			if baseline.TopologyCounts[fp] > 1 {
				baseline.TopologyCounts[fp] = int64(float64(baseline.TopologyCounts[fp]) * decayWeight)
			}
		}
	}

	// Initialize if new fingerprint
	if _, exists := baseline.TopologyCounts[fingerprint]; !exists {
		baseline.TopologyCounts[fingerprint] = 1
		lastSeenAt[fingerprint] = now
	}
}

// lastSeenMap returns the last-seen map after giving any pending imported
// paths now as their last-seen, so their decay starts at this trace.
func (s *ServiceBaseline) lastSeenMap(now time.Time) map[string]time.Time {
	if s.topologyLastSeen == nil {
		s.topologyLastSeen = make(map[string]time.Time)
	}
	for _, fp := range s.lastSeenPending {
		if _, ok := s.topologyLastSeen[fp]; !ok {
			s.topologyLastSeen[fp] = now
		}
	}
	s.lastSeenPending = nil
	return s.topologyLastSeen
}

// YesterdayPathBaseline merges the path across yesterday's same hour +-2h, or nil.
func (bc *BaselineComputer) YesterdayPathBaseline(serviceID, fingerprint string, t time.Time) *PathBaseline {
	h := bc.hourlyHistory()
	if h == nil {
		return nil
	}
	return h.PathBaseline(serviceID, fingerprint, t)
}

// YesterdayFrequency returns the path count and total of yesterday's busiest
// comparison bucket; ok is false without one.
func (bc *BaselineComputer) YesterdayFrequency(serviceID, fingerprint string, t time.Time) (count, total int64, ok bool) {
	h := bc.hourlyHistory()
	if h == nil {
		return 0, 0, false
	}
	return h.Frequency(serviceID, fingerprint, t)
}

// updatePathBaselineVariant updates path baseline using variant-specific logic
func (bc *BaselineComputer) updatePathBaselineVariant(baseline *ServiceBaseline, fingerprint string, duration int64, traceTime time.Time) {
	pb, exists := baseline.PathBaselines[fingerprint]
	if !exists {
		pb = &PathBaseline{
			MinDuration: duration,
			MaxDuration: duration,
		}
		baseline.PathBaselines[fingerprint] = pb
	}

	switch bc.variant {
	case VariantEWMA:
		// EWMA for duration: smooth the mean duration
		if pb.Count == 0 {
			pb.SumDuration = duration
			pb.Count = 1
		} else {
			// EWMA update: new_mean = alpha * current + (1-alpha) * previous_mean
			currentMean := float64(duration)
			previousMean := float64(pb.SumDuration) / float64(pb.Count)
			newMean := bc.baselineAlpha*currentMean + (1-bc.baselineAlpha)*previousMean
			pb.SumDuration = int64(newMean * float64(pb.Count+1))
			pb.Count++
		}
		pb.Samples = appendCapped(pb.Samples, duration, 1000, pb.Count)

	case VariantSlidingWindow:
		// For sliding window, use time-based decay on path baselines
		// Decay old samples based on time elapsed
		now := traceTime
		cutoff := now.Add(-bc.slidingWindowDuration)

		// Track last update time for this path baseline
		if pb.LastUpdateTime.IsZero() {
			pb.LastUpdateTime = now
		}

		age := now.Sub(pb.LastUpdateTime)
		if age > bc.slidingWindowDuration {
			// Path hasn't been seen in window - reset or decay significantly
			decayFactor := 0.5 // Heavy decay for paths outside window
			pb.Count = int64(float64(pb.Count) * decayFactor)
			pb.SumDuration = int64(float64(pb.SumDuration) * decayFactor)
		} else if age > 0 {
			// Apply time-based decay proportional to age
			ageRatio := float64(age) / float64(bc.slidingWindowDuration)
			decayFactor := 1.0 - (ageRatio * 0.05) // Decay up to 5% per window duration
			if pb.Count > 1 {
				pb.Count = int64(float64(pb.Count) * decayFactor)
				pb.SumDuration = int64(float64(pb.SumDuration) * decayFactor)
			}
		}

		// Update with current trace
		pb.Count++
		pb.SumDuration += duration
		if duration < pb.MinDuration || pb.MinDuration == 0 {
			pb.MinDuration = duration
		}
		if duration > pb.MaxDuration {
			pb.MaxDuration = duration
		}
		pb.LastUpdateTime = now

		pb.Samples = appendCapped(pb.Samples, duration, 1000, pb.Count)

		_ = cutoff // Keep for reference

	case VariantExponentialDecay:
		// For exponential decay, use standard update but periodically decay
		pb.LastUpdateTime = traceTime
		pb.Count++
		pb.SumDuration += duration
		if duration < pb.MinDuration {
			pb.MinDuration = duration
		}
		if duration > pb.MaxDuration {
			pb.MaxDuration = duration
		}
		pb.Samples = appendCapped(pb.Samples, duration, 1000, pb.Count)

		// Periodically decay for exponential decay
		if pb.Count%100 == 0 {
			decayFactor := 0.99
			if bc.variant == VariantExponentialDecay {
				// More aggressive decay for exponential decay
				decayFactor = 0.95
			}
			pb.SumDuration = int64(float64(pb.SumDuration) * decayFactor)
			if pb.Count > 100 {
				pb.Count = int64(float64(pb.Count) * decayFactor)
			}
		}

	default: // VariantCumulative
		// Standard cumulative update (existing logic)
		pb.Count++
		pb.SumDuration += duration
		if duration < pb.MinDuration {
			pb.MinDuration = duration
		}
		if duration > pb.MaxDuration {
			pb.MaxDuration = duration
		}
		pb.Samples = appendCapped(pb.Samples, duration, 1000, pb.Count)
	}
}

// ExportBaseline exports a service baseline for persistence
func (bc *BaselineComputer) ExportBaseline(serviceID string) *ExportedBaseline {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	baseline, exists := bc.services[serviceID]
	if !exists {
		return nil
	}

	// Export path baselines
	pathBaselines := make(map[string]*ExportedPathBaseline)
	for fp, pb := range baseline.PathBaselines {
		pathBaselines[fp] = &ExportedPathBaseline{
			Count:          pb.Count,
			SumDuration:    pb.SumDuration,
			MinDuration:    pb.MinDuration,
			MaxDuration:    pb.MaxDuration,
			Samples:        append([]int64{}, pb.Samples...), // Copy slice
			LastUpdateTime: pb.LastUpdateTime,
		}
	}

	// Export branch baselines
	branchBaselines := make(map[string]*ExportedBranchBaseline)
	for key, bb := range baseline.BranchBaselines {
		branchBaselines[key] = &ExportedBranchBaseline{
			Count:       bb.Count,
			SumDuration: bb.SumDuration,
			MinDuration: bb.MinDuration,
			MaxDuration: bb.MaxDuration,
			ErrorCount:  bb.ErrorCount,
			Samples:     append([]int64{}, bb.Samples...), // Copy slice
		}
	}

	// Copy maps to avoid race conditions
	topologyCounts := make(map[string]int64)
	for k, v := range baseline.TopologyCounts {
		topologyCounts[k] = v
	}

	topologyFirstSeen := make(map[string]time.Time)
	for k, v := range baseline.TopologyFirstSeen {
		topologyFirstSeen[k] = v
	}

	knownGoodPaths := make(map[string]bool)
	for k, v := range baseline.KnownGoodPaths {
		knownGoodPaths[k] = v
	}

	topologyFreqEWMA := make(map[string]float64)
	for k, v := range baseline.TopologyFrequenciesEWMA {
		topologyFreqEWMA[k] = v
	}

	return &ExportedBaseline{
		ServiceID:               serviceID,
		ServiceGrouping:         baseline.ServiceGrouping,
		TopologyCounts:          topologyCounts,
		TopologyFirstSeen:       topologyFirstSeen,
		KnownGoodPaths:          knownGoodPaths,
		CanonicalFingerprint:    baseline.CanonicalFingerprint,
		TotalTraces:             baseline.TotalTraces,
		ErrorCount:              baseline.ErrorCount,
		DurationSum:             baseline.DurationSum,
		DurationMin:             baseline.DurationMin,
		DurationMax:             baseline.DurationMax,
		DurationSamples:         append([]int64(nil), baseline.DurationSamples...),
		PathBaselines:           pathBaselines,
		BranchBaselines:         branchBaselines,
		TopologyFrequenciesEWMA: topologyFreqEWMA,
		LastUpdateTime:          baseline.LastUpdateTime,
		LastSeen:                baseline.LastSeen,
		Clock:                   TraceClock,
	}
}

// TraceClock marks an export whose first-seen and path LastUpdateTime stamps
// are trace times. Rows without it were written when those stamps (for some
// variants) came from the wall clock.
const TraceClock = "trace"

// ExportedBaseline represents serializable baseline data. Not persisted:
// TopologyExamples, RecentWindow and PreviousFrequencies. TopologyFirstSeen and
// path LastUpdateTime hold trace times when Clock is TraceClock; LastUpdateTime
// and LastSeen hold wall time. The sliding_window and exponential_decay
// last-seen map is rebuilt on import from those stamps.
type ExportedBaseline struct {
	ServiceID               string                             `json:"service_id"`
	ServiceGrouping         models.ServiceGrouping             `json:"service_grouping"`
	TopologyCounts          map[string]int64                   `json:"topology_counts"`
	TopologyFirstSeen       map[string]time.Time               `json:"topology_first_seen"`
	KnownGoodPaths          map[string]bool                    `json:"known_good_paths"`
	CanonicalFingerprint    string                             `json:"canonical_fingerprint"`
	TotalTraces             int64                              `json:"total_traces"`
	ErrorCount              int64                              `json:"error_count"`
	DurationSum             int64                              `json:"duration_sum"`
	DurationMin             int64                              `json:"duration_min"`
	DurationMax             int64                              `json:"duration_max"`
	DurationSamples         []int64                            `json:"duration_samples,omitempty"`
	PathBaselines           map[string]*ExportedPathBaseline   `json:"path_baselines"`
	BranchBaselines         map[string]*ExportedBranchBaseline `json:"branch_baselines"`
	TopologyFrequenciesEWMA map[string]float64                 `json:"topology_frequencies_ewma"`
	LastUpdateTime          time.Time                          `json:"last_update_time"`
	LastSeen                time.Time                          `json:"last_seen"`
	Clock                   string                             `json:"clock,omitempty"`
}

// ExportedPathBaseline represents serializable path baseline
type ExportedPathBaseline struct {
	Count          int64     `json:"count"`
	SumDuration    int64     `json:"sum_duration"`
	MinDuration    int64     `json:"min_duration"`
	MaxDuration    int64     `json:"max_duration"`
	Samples        []int64   `json:"samples"`
	LastUpdateTime time.Time `json:"last_update_time,omitempty"`
}

// ExportedBranchBaseline represents serializable branch baseline
type ExportedBranchBaseline struct {
	Count       int64   `json:"count"`
	SumDuration int64   `json:"sum_duration"`
	MinDuration int64   `json:"min_duration"`
	MaxDuration int64   `json:"max_duration"`
	ErrorCount  int64   `json:"error_count"`
	Samples     []int64 `json:"samples"`
}

// ImportBaseline imports a persisted baseline into memory
func (bc *BaselineComputer) ImportBaseline(data *ExportedBaseline) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Reconstruct path baselines
	pathBaselines := make(map[string]*PathBaseline)
	for fp, epb := range data.PathBaselines {
		pathBaselines[fp] = &PathBaseline{
			Count:          epb.Count,
			SumDuration:    epb.SumDuration,
			MinDuration:    epb.MinDuration,
			MaxDuration:    epb.MaxDuration,
			Samples:        append([]int64{}, epb.Samples...),
			LastUpdateTime: epb.LastUpdateTime,
		}
	}

	// Reconstruct branch baselines
	branchBaselines := make(map[string]*BranchBaseline)
	for key, ebb := range data.BranchBaselines {
		branchBaselines[key] = &BranchBaseline{
			Count:       ebb.Count,
			SumDuration: ebb.SumDuration,
			MinDuration: ebb.MinDuration,
			MaxDuration: ebb.MaxDuration,
			ErrorCount:  ebb.ErrorCount,
			Samples:     append([]int64{}, ebb.Samples...),
		}
	}

	// Rows saved before DurationSamples was persisted rebuild an approximation
	// from up to 10 samples per path.
	durationSamples := append([]int64(nil), data.DurationSamples...)
	if len(durationSamples) == 0 {
		for _, pb := range pathBaselines {
			if len(pb.Samples) > 0 && len(durationSamples) < 1000 {
				take := min(10, len(pb.Samples))
				durationSamples = append(durationSamples, pb.Samples[:take]...)
			}
		}
	}

	// One export seeds every combination, so each needs its own maps.
	topologyCounts := make(map[string]int64, len(data.TopologyCounts))
	for k, v := range data.TopologyCounts {
		topologyCounts[k] = v
	}
	// A row without the trace clock marker may hold wall-clock first-seen
	// stamps, which would make its paths look new to replayed older traces.
	// Every path in a stored row was seen before the save, so it gets the zero
	// time, which IsNewPath treats as seen long ago.
	legacyClock := data.Clock != TraceClock
	topologyFirstSeen := make(map[string]time.Time, len(data.TopologyFirstSeen))
	for k, v := range data.TopologyFirstSeen {
		if legacyClock {
			v = time.Time{}
		}
		topologyFirstSeen[k] = v
	}
	knownGoodPaths := make(map[string]bool, len(data.KnownGoodPaths))
	for k, v := range data.KnownGoodPaths {
		knownGoodPaths[k] = v
	}
	topologyFreqEWMA := make(map[string]float64, len(data.TopologyFrequenciesEWMA))
	for k, v := range data.TopologyFrequenciesEWMA {
		topologyFreqEWMA[k] = v
	}

	// Last-seen is the later of first-seen and the path's LastUpdateTime. A
	// path with neither (legacy rows) waits for the next trace to stamp it, so
	// it is not decayed by its age against the zero time.
	var lastSeen map[string]time.Time
	var lastSeenPending []string
	if bc.variant == VariantSlidingWindow || bc.variant == VariantExponentialDecay {
		lastSeen = make(map[string]time.Time, len(topologyFirstSeen))
		for fp, first := range topologyFirstSeen {
			last := first
			if pb := pathBaselines[fp]; pb != nil && pb.LastUpdateTime.After(last) {
				last = pb.LastUpdateTime
			}
			if last.IsZero() {
				lastSeenPending = append(lastSeenPending, fp)
				continue
			}
			lastSeen[fp] = last
		}
	}

	bc.services[data.ServiceID] = &ServiceBaseline{
		ServiceID:               data.ServiceID,
		ServiceGrouping:         data.ServiceGrouping,
		TopologyCounts:          topologyCounts,
		TopologyExamples:        make(map[string]*models.Trace), // Will be repopulated
		TopologyFirstSeen:       topologyFirstSeen,
		KnownGoodPaths:          knownGoodPaths,
		CanonicalFingerprint:    data.CanonicalFingerprint,
		TotalTraces:             data.TotalTraces,
		ErrorCount:              data.ErrorCount,
		DurationSum:             data.DurationSum,
		DurationMin:             data.DurationMin,
		DurationMax:             data.DurationMax,
		DurationSamples:         durationSamples,
		PathBaselines:           pathBaselines,
		BranchBaselines:         branchBaselines,
		TopologyFrequenciesEWMA: topologyFreqEWMA,
		RecentWindow:            make(map[string]int64),
		PreviousFrequencies:     make(map[string]float64),
		LastUpdateTime:          data.LastUpdateTime,
		LastSeen:                data.LastSeen,
		topologyLastSeen:        lastSeen,
		lastSeenPending:         lastSeenPending,
	}
}

// GetAllServiceIDs returns all tracked service IDs
func (bc *BaselineComputer) GetAllServiceIDs() []string {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	ids := make([]string, 0, len(bc.services))
	for id := range bc.services {
		ids = append(ids, id)
	}
	return ids
}
