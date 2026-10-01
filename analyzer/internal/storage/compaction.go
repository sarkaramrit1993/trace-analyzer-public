package storage

import (
	"context"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/trace-analyzer/internal/models"
)

// rollupWithSamples holds rollup data with duration samples for percentile calculation
type rollupWithSamples struct {
	rollup          *models.Rollup
	durationSamples []int64 // reservoir sample of durations
}

// CompactionService handles moving data between tiers
type CompactionService struct {
	hot            *SQLiteStorage
	cold           *ColdTierStorage
	hotRetention   time.Duration
	warmRetention  time.Duration
	rollupInterval time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup

	// In-memory rollup aggregation with samples for percentiles
	rollupBuffer map[string]*rollupWithSamples // key: serviceID:fingerprint:windowStart
	rollupMu     sync.Mutex

	started bool // guard against double-start
}

// NewCompactionService creates a new compaction service
func NewCompactionService(
	hot *SQLiteStorage,
	cold *ColdTierStorage,
	hotRetention time.Duration,
	warmRetention time.Duration,
	rollupInterval time.Duration,
) *CompactionService {
	ctx, cancel := context.WithCancel(context.Background())
	return &CompactionService{
		hot:            hot,
		cold:           cold,
		hotRetention:   hotRetention,
		warmRetention:  warmRetention,
		rollupInterval: rollupInterval,
		ctx:            ctx,
		cancel:         cancel,
		rollupBuffer:   make(map[string]*rollupWithSamples),
	}
}

// Start begins the compaction background loops
func (c *CompactionService) Start() {
	c.rollupMu.Lock()
	if c.started {
		c.rollupMu.Unlock()
		log.Warn().Msg("Compaction service already started")
		return
	}
	c.started = true
	c.rollupMu.Unlock()

	// Hot to warm compaction (cleanup old hot data)
	c.wg.Add(1)
	go c.hotCleanupLoop()

	// Warm to cold compaction (write gzipped JSON to S3)
	if c.cold != nil {
		c.wg.Add(1)
		go c.coldCompactionLoop()
	}

	// Rollup aggregation loop
	c.wg.Add(1)
	go c.rollupLoop()

	log.Info().
		Dur("hot_retention", c.hotRetention).
		Dur("warm_retention", c.warmRetention).
		Dur("rollup_interval", c.rollupInterval).
		Msg("Compaction service started")
}

// Stop gracefully shuts down the compaction service
func (c *CompactionService) Stop() {
	c.cancel()
	c.wg.Wait()
	log.Info().Msg("Compaction service stopped")
}

const maxDurationSamples = 1000 // reservoir size for percentile calculation
const maxRollupSampleIDs = 10

// AddToRollup adds a trace to the current rollup window
func (c *CompactionService) AddToRollup(trace *models.Trace, fingerprint string) {
	c.rollupMu.Lock()
	defer c.rollupMu.Unlock()

	// Determine rollup window
	windowStart := trace.StartTime.Truncate(c.rollupInterval)
	windowEnd := windowStart.Add(c.rollupInterval)

	key := trace.ServiceID + ":" + fingerprint + ":" + windowStart.Format(time.RFC3339)

	rws, exists := c.rollupBuffer[key]
	if !exists {
		rws = &rollupWithSamples{
			rollup: &models.Rollup{
				WindowStart:     windowStart,
				WindowEnd:       windowEnd,
				ServiceID:       trace.ServiceID,
				ServiceGrouping: trace.ServiceGrouping,
				Fingerprint:     fingerprint,
				DurationMin:     trace.TotalDurationUs,
				DurationMax:     trace.TotalDurationUs,
				BranchStats:     make(map[string]*models.BranchStats),
				SampleTraceIDs:  []string{},
			},
			durationSamples: make([]int64, 0, maxDurationSamples),
		}
		c.rollupBuffer[key] = rws
	}

	rollup := rws.rollup

	// Update rollup statistics
	rollup.TraceCount++
	rollup.SpanCount += int64(len(trace.Spans))
	rollup.DurationSum += trace.TotalDurationUs

	if trace.TotalDurationUs < rollup.DurationMin {
		rollup.DurationMin = trace.TotalDurationUs
	}
	if trace.TotalDurationUs > rollup.DurationMax {
		rollup.DurationMax = trace.TotalDurationUs
	}

	if trace.HasError {
		rollup.ErrorCount++
	}

	// Reservoir sampling for duration percentiles
	if len(rws.durationSamples) < maxDurationSamples {
		rws.durationSamples = append(rws.durationSamples, trace.TotalDurationUs)
	} else {
		// Replace with probability maxDurationSamples/traceCount
		idx := rand.Int63n(rollup.TraceCount)
		if idx < maxDurationSamples {
			rws.durationSamples[idx] = trace.TotalDurationUs
		}
	}

	// Keep sample trace IDs (max 10)
	if len(rollup.SampleTraceIDs) < maxRollupSampleIDs {
		rollup.SampleTraceIDs = append(rollup.SampleTraceIDs, trace.TraceID)
	}

	// Update branch stats
	for _, span := range trace.Spans {
		branchKey := span.ServiceName + ":" + span.OperationName
		bs, exists := rollup.BranchStats[branchKey]
		if !exists {
			bs = &models.BranchStats{
				MinDuration: span.Duration,
				MaxDuration: span.Duration,
			}
			rollup.BranchStats[branchKey] = bs
		}

		bs.Count++
		bs.SumDuration += span.Duration
		if span.Duration < bs.MinDuration {
			bs.MinDuration = span.Duration
		}
		if span.Duration > bs.MaxDuration {
			bs.MaxDuration = span.Duration
		}
		if span.GetStatus() == "ERROR" {
			bs.ErrorCount++
		}
	}
}

func (c *CompactionService) hotCleanupLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if err := c.hot.Cleanup(c.hotRetention); err != nil {
				log.Error().Err(err).Msg("Hot tier cleanup error")
			}
		}
	}
}

func (c *CompactionService) rollupLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.rollupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			// Flush everything, including the window still in progress
			c.flushRollups(true)
			return
		case <-ticker.C:
			c.flushRollups(false)
		}
	}
}

// flushRollups stores completed windows, or every buffered window when force is set.
func (c *CompactionService) flushRollups(force bool) {
	c.rollupMu.Lock()

	// Find rollups that are complete (window end is in the past)
	now := time.Now()
	var toFlush []*models.Rollup
	var keysToDelete []string

	for key, rws := range c.rollupBuffer {
		if force || rws.rollup.WindowEnd.Before(now) {
			// Calculate percentiles from reservoir samples
			if len(rws.durationSamples) > 0 {
				rws.rollup.DurationP50 = percentile(rws.durationSamples, 0.50)
				rws.rollup.DurationP99 = percentile(rws.durationSamples, 0.99)
			}
			toFlush = append(toFlush, rws.rollup)
			keysToDelete = append(keysToDelete, key)
		}
	}

	for _, key := range keysToDelete {
		delete(c.rollupBuffer, key)
	}

	c.rollupMu.Unlock()

	if len(toFlush) == 0 {
		return
	}

	// Store rollups in warm tier (SQLite)
	for _, rollup := range toFlush {
		if err := c.hot.StoreRollup(rollup); err != nil {
			log.Error().Err(err).Msg("Failed to store rollup in warm tier")
		}
	}

	log.Info().Int("rollups", len(toFlush)).Msg("Flushed rollups to warm tier")
}

// percentile calculates the p-th percentile from a sample
func percentile(samples []int64, p float64) int64 {
	if len(samples) == 0 {
		return 0
	}

	sorted := make([]int64, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func (c *CompactionService) coldCompactionLoop() {
	defer c.wg.Done()

	// Run cold compaction every hour
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.compactToCold()
		}
	}
}

func (c *CompactionService) compactToCold() {
	if c.cold == nil {
		return
	}

	// Get rollups older than warm retention
	cutoff := time.Now().Add(-c.warmRetention)

	rollups, err := c.hot.GetRollupsBeforeCutoff(cutoff)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get rollups for cold compaction")
		return
	}

	if len(rollups) == 0 {
		return
	}

	// Write to cold storage (S3-compatible)
	if err := c.cold.WriteRollups(rollups); err != nil {
		log.Error().Err(err).Msg("Failed to write rollups to cold storage")
		return
	}

	// Delete from warm tier after successful cold write
	deleted, err := c.hot.DeleteRollupsBeforeCutoff(cutoff)
	if err != nil {
		log.Error().Err(err).Msg("Failed to delete old rollups from warm tier")
		return
	}

	log.Info().
		Int("rollups", len(rollups)).
		Int64("deleted", deleted).
		Time("cutoff", cutoff).
		Msg("Compacted rollups to cold tier")
}

// Stats returns compaction statistics
func (c *CompactionService) Stats() (pendingRollups int, coldObjects int64, coldBytes int64) {
	c.rollupMu.Lock()
	pendingRollups = len(c.rollupBuffer)
	c.rollupMu.Unlock()

	if c.cold != nil {
		coldObjects, coldBytes, _ = c.cold.Stats()
	}

	return
}
