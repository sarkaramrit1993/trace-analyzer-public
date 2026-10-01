package analysis

import (
	"math/rand"
	"sort"
	"sync"
	"time"
)

// DefaultHourlyPathSampleCap is the per (hour, service, path) reservoir size.
const DefaultHourlyPathSampleCap = 200

const hourKeyLayout = "2006-01-02-15"

// hourlyRetentionHorizon is how far ahead retainedHourKeys looks. Friday's
// buckets serve Monday, three days later, so four days covers every lookup.
const hourlyRetentionHorizon = 4 * 24 * time.Hour

// HourlyHistory holds per-hour, per-service traffic for same_time_yesterday.
// One instance is shared by every same_time_yesterday combination.
type HourlyHistory struct {
	mu        sync.RWMutex
	sampleCap int
	buckets   map[string]map[string]*hourlyBucket // hour key -> service -> bucket
	latest    time.Time                           // latest trace time seen
	latestKey string                              // hourKey(latest)
	retained  map[string]bool                     // retainedHourKeys(latest)
	dirty     map[bucketID]struct{}
	rng       *rand.Rand // fixed seed so replays pick the same reservoir samples
}

type bucketID struct{ hour, service string }

type hourlyBucket struct {
	TotalTraces    int64
	TopologyCounts map[string]int64
	Paths          map[string]*hourlyPath
}

type hourlyPath struct {
	Count, Sum, Min, Max int64
	Samples              []int64
}

// ExportedHourlyBucket is one (hour, service) bucket in serializable form.
type ExportedHourlyBucket struct {
	Hour           string                           `json:"hour"`
	ServiceID      string                           `json:"service_id"`
	TotalTraces    int64                            `json:"total_traces"`
	TopologyCounts map[string]int64                 `json:"topology_counts"`
	Paths          map[string]*ExportedPathBaseline `json:"paths"`
}

// NewHourlyHistory creates an empty history; sampleCap <= 0 uses the default.
func NewHourlyHistory(sampleCap int) *HourlyHistory {
	if sampleCap <= 0 {
		sampleCap = DefaultHourlyPathSampleCap
	}
	return &HourlyHistory{
		sampleCap: sampleCap,
		buckets:   make(map[string]map[string]*hourlyBucket),
		dirty:     make(map[bucketID]struct{}),
		rng:       rand.New(rand.NewSource(1)),
	}
}

func hourKey(t time.Time) string {
	return t.UTC().Format(hourKeyLayout)
}

// comparisonHourKeys returns the buckets a trace at t is compared against:
// yesterday's same hour +-2h, or Friday's when yesterday is a weekend day.
func comparisonHourKeys(t time.Time) []string {
	yesterday := t.UTC().Truncate(time.Hour).AddDate(0, 0, -1)
	switch yesterday.Weekday() {
	case time.Saturday:
		yesterday = yesterday.AddDate(0, 0, -1)
	case time.Sunday:
		yesterday = yesterday.AddDate(0, 0, -2)
	}
	keys := make([]string, 0, 5)
	for offset := -2; offset <= 2; offset++ {
		keys = append(keys, hourKey(yesterday.Add(time.Duration(offset)*time.Hour)))
	}
	return keys
}

// retainedHourKeys is every bucket some lookup in [now, now+4d] can reach.
func retainedHourKeys(now time.Time) map[string]bool {
	keys := make(map[string]bool)
	start := now.UTC().Truncate(time.Hour)
	for d := time.Duration(0); d <= hourlyRetentionHorizon; d += time.Hour {
		for _, k := range comparisonHourKeys(start.Add(d)) {
			keys[k] = true
		}
	}
	return keys
}

// Add records one trace. A newer hour than any seen before drops buckets no
// future lookup can reach; traces for such hours are not stored. A trace more
// than MaxFutureSkew ahead of the wall clock never moves retention.
func (h *HourlyHistory) Add(serviceID, fingerprint string, durationUs int64, t time.Time) {
	hour := hourKey(t)
	h.mu.Lock()
	defer h.mu.Unlock()

	if (h.retained == nil || t.After(h.latest)) && !tooFarAhead(t) {
		if h.retained == nil || hour != h.latestKey {
			h.advanceLocked(t)
		} else {
			h.latest = t
		}
	}
	if !h.retained[hour] {
		return
	}

	b := h.bucketLocked(hour, serviceID)
	b.TotalTraces++
	b.TopologyCounts[fingerprint]++
	p := b.Paths[fingerprint]
	if p == nil {
		p = &hourlyPath{Min: durationUs, Max: durationUs}
		b.Paths[fingerprint] = p
	}
	p.Count++
	p.Sum += durationUs
	p.Min = min(p.Min, durationUs)
	p.Max = max(p.Max, durationUs)
	if len(p.Samples) < h.sampleCap {
		p.Samples = appendCapped(p.Samples, durationUs, h.sampleCap, 0)
	} else if j := h.rng.Int63n(p.Count); j < int64(h.sampleCap) {
		p.Samples[j] = durationUs
	}
	h.dirty[bucketID{hour, serviceID}] = struct{}{}
}

func (h *HourlyHistory) advanceLocked(t time.Time) {
	h.latest, h.latestKey = t, hourKey(t)
	h.retained = retainedHourKeys(t)
	for hour := range h.buckets {
		if !h.retained[hour] {
			for svc := range h.buckets[hour] {
				delete(h.dirty, bucketID{hour, svc})
			}
			delete(h.buckets, hour)
		}
	}
}

func (h *HourlyHistory) bucketLocked(hour, serviceID string) *hourlyBucket {
	svcs := h.buckets[hour]
	if svcs == nil {
		svcs = make(map[string]*hourlyBucket)
		h.buckets[hour] = svcs
	}
	b := svcs[serviceID]
	if b == nil {
		b = &hourlyBucket{TopologyCounts: make(map[string]int64), Paths: make(map[string]*hourlyPath)}
		svcs[serviceID] = b
	}
	return b
}

// bestBucketLocked is the comparison bucket with the most traces.
func (h *HourlyHistory) bestBucketLocked(serviceID string, t time.Time) *hourlyBucket {
	var best *hourlyBucket
	for _, k := range comparisonHourKeys(t) {
		if b := h.buckets[k][serviceID]; b != nil && (best == nil || b.TotalTraces > best.TotalTraces) {
			best = b
		}
	}
	return best
}

// Frequency returns the path's count and the total from the busiest
// comparison bucket for t. ok is false when there is none.
func (h *HourlyHistory) Frequency(serviceID, fingerprint string, t time.Time) (count, total int64, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	b := h.bestBucketLocked(serviceID, t)
	if b == nil {
		return 0, 0, false
	}
	return b.TopologyCounts[fingerprint], b.TotalTraces, true
}

// PathBaseline merges the path across every comparison bucket for t into a
// fresh PathBaseline, or returns nil when none of them saw the path. Each hour
// keeps at most sampleCap samples, so when more than one hour contributes each
// sample is weighted by its hour's Count/len(Samples) and percentiles reflect
// every hour's true traffic. Count, Min, Max and Mean are exact.
func (h *HourlyHistory) PathBaseline(serviceID, fingerprint string, t time.Time) *PathBaseline {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var pb *PathBaseline
	hours := 0
	for _, k := range comparisonHourKeys(t) {
		b := h.buckets[k][serviceID]
		if b == nil {
			continue
		}
		p := b.Paths[fingerprint]
		if p == nil {
			continue
		}
		if pb == nil {
			pb = &PathBaseline{MinDuration: p.Min, MaxDuration: p.Max}
		}
		pb.Count += p.Count
		pb.SumDuration += p.Sum
		pb.MinDuration = min(pb.MinDuration, p.Min)
		pb.MaxDuration = max(pb.MaxDuration, p.Max)
		if len(p.Samples) == 0 {
			continue
		}
		w := float64(p.Count) / float64(len(p.Samples))
		for range p.Samples {
			pb.weights = append(pb.weights, w)
		}
		pb.Samples = append(pb.Samples, p.Samples...)
		hours++
	}
	if hours < 2 && pb != nil {
		pb.weights = nil
	}
	return pb
}

// Export copies buckets sorted by (hour, service). With onlyDirty it returns
// only buckets changed since their last export. Exported buckets are marked clean.
func (h *HourlyHistory) Export(onlyDirty bool) []ExportedHourlyBucket {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []ExportedHourlyBucket
	for hour, svcs := range h.buckets {
		for svc, b := range svcs {
			id := bucketID{hour, svc}
			if _, dirty := h.dirty[id]; onlyDirty && !dirty {
				continue
			}
			delete(h.dirty, id)
			e := ExportedHourlyBucket{
				Hour:           hour,
				ServiceID:      svc,
				TotalTraces:    b.TotalTraces,
				TopologyCounts: make(map[string]int64, len(b.TopologyCounts)),
				Paths:          make(map[string]*ExportedPathBaseline, len(b.Paths)),
			}
			for fp, n := range b.TopologyCounts {
				e.TopologyCounts[fp] = n
			}
			for fp, p := range b.Paths {
				e.Paths[fp] = &ExportedPathBaseline{Count: p.Count, SumDuration: p.Sum, MinDuration: p.Min, MaxDuration: p.Max, Samples: append([]int64{}, p.Samples...)}
			}
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hour != out[j].Hour {
			return out[i].Hour < out[j].Hour
		}
		return out[i].ServiceID < out[j].ServiceID
	})
	return out
}

// Import replaces buckets with persisted ones. Retention is judged from the
// newest of the latest trace seen and the newest imported hour that is not
// more than MaxFutureSkew ahead of the wall clock; buckets outside it are
// dropped. Imported buckets start clean.
func (h *HourlyHistory) Import(buckets []ExportedHourlyBucket) {
	h.mu.Lock()
	defer h.mu.Unlock()
	newest := h.latest
	for _, e := range buckets {
		if t, err := time.Parse(hourKeyLayout, e.Hour); err == nil && t.After(newest) && !tooFarAhead(t) {
			newest = t
		}
	}
	if newest.IsZero() {
		return
	}
	if h.retained == nil || hourKey(newest) != h.latestKey {
		h.advanceLocked(newest)
	}
	for _, e := range buckets {
		if !h.retained[e.Hour] {
			continue
		}
		b := &hourlyBucket{
			TotalTraces:    e.TotalTraces,
			TopologyCounts: make(map[string]int64, len(e.TopologyCounts)),
			Paths:          make(map[string]*hourlyPath, len(e.Paths)),
		}
		for fp, n := range e.TopologyCounts {
			b.TopologyCounts[fp] = n
		}
		for fp, p := range e.Paths {
			samples := p.Samples
			if len(samples) > h.sampleCap {
				samples = samples[:h.sampleCap]
			}
			b.Paths[fp] = &hourlyPath{Count: p.Count, Sum: p.SumDuration, Min: p.MinDuration, Max: p.MaxDuration, Samples: append([]int64(nil), samples...)}
		}
		if h.buckets[e.Hour] == nil {
			h.buckets[e.Hour] = make(map[string]*hourlyBucket)
		}
		h.buckets[e.Hour][e.ServiceID] = b
		delete(h.dirty, bucketID{e.Hour, e.ServiceID})
	}
}

// RetainedHourKeys lists, sorted, the hours a future lookup can still reach.
func (h *HourlyHistory) RetainedHourKeys() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	keys := make([]string, 0, len(h.retained))
	for k := range h.retained {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// stats returns the number of (hour, service) buckets and stored samples.
func (h *HourlyHistory) stats() (buckets, samples int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, svcs := range h.buckets {
		buckets += len(svcs)
		for _, b := range svcs {
			for _, p := range b.Paths {
				samples += len(p.Samples)
			}
		}
	}
	return buckets, samples
}
