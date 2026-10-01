package ingestion

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/trace-analyzer/internal/models"
	"github.com/trace-analyzer/internal/storage"
)

// TraceHandler is called when a trace is emitted (after 3-minute window)
type TraceHandler func(trace *models.Trace) error

// PendingTrace holds spans being assembled
type PendingTrace struct {
	TraceID       string
	Spans         map[string]*models.Span // spanID -> span (for deduplication, keep latest)
	FirstSeen     time.Time
	LastSeen      time.Time
	TimerExpiry   time.Time // When this trace should be emitted
	Version       int64     // Incremented on each span add, used to detect concurrent modifications
	BeingEvicted  bool      // True while trace is being written to disk during eviction
	WrittenToDisk bool      // True if spans have been persisted to SQLite
}

// TraceAssembler collects spans and emits traces after 3-minute window
//
// Logic:
// - Track unique trace-ids in LRU (3-minute TTL)
// - When new trace-id seen, start 3-minute timer
// - Store spans in memory for 1 minute, then write to SQLite
// - On timer expiry (3 minutes), read from SQLite, merge all spans, emit
// - Handle duplicates (keep latest span)
// - Emit all spans after 3 minutes (no completeness checks)
type TraceAssembler struct {
	windowDuration time.Duration // 3 minutes
	memoryDuration time.Duration // 1 minute (keep in memory before writing to disk)
	handler        TraceHandler
	pending        map[string]*PendingTrace // traceID -> pending trace (in-memory)
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	identityFields []string
	tagKeys        models.GroupingTagKeys
	storage        storage.Storage // For disk persistence

	tracesEmitted   int64
	spansReceived   int64
	maxMemoryTraces int // Maximum trace-ids to hold in memory
}

// NewTraceAssembler creates a new assembler with 3-minute window
func NewTraceAssembler(windowDuration time.Duration, identityFields []string, handler TraceHandler, store storage.Storage) *TraceAssembler {
	ctx, cancel := context.WithCancel(context.Background())
	return &TraceAssembler{
		windowDuration:  windowDuration, // Default: 3 minutes
		memoryDuration:  time.Minute,    // Keep in memory for 1 minute
		handler:         handler,
		pending:         make(map[string]*PendingTrace),
		ctx:             ctx,
		cancel:          cancel,
		identityFields:  identityFields,
		tagKeys:         models.DefaultGroupingTagKeys(),
		storage:         store,
		maxMemoryTraces: 10000, // Max trace-ids in memory
	}
}

// SetGroupingTagKeys sets the tag keys read for each grouping dimension.
// Empty lists fall back to the defaults. Call before Start.
func (a *TraceAssembler) SetGroupingTagKeys(keys models.GroupingTagKeys) {
	a.tagKeys = keys.WithDefaults()
}

// Start begins the timer and batch write loops
func (a *TraceAssembler) Start() {
	a.wg.Add(3)
	go a.timerLoop()       // Check for expired timers every second
	go a.batchWriteLoop()  // Write to disk every minute
	go a.lruEvictionLoop() // Evict old trace-ids from LRU every 10 seconds
	log.Info().Dur("window", a.windowDuration).Dur("memory", a.memoryDuration).Msg("Trace assembler started")
}

// Stop gracefully shuts down the assembler
func (a *TraceAssembler) Stop() {
	a.cancel()
	a.wg.Wait()

	// Emit all pending traces
	a.emitAllPending()

	log.Info().Int64("traces", a.tracesEmitted).Msg("Trace assembler stopped")
}

// emitAllPending emits all traces from memory and SQLite during shutdown
func (a *TraceAssembler) emitAllPending() {
	// Collect all trace IDs (snapshot under lock)
	a.mu.Lock()
	traceIDs := make([]string, 0, len(a.pending))
	for traceID := range a.pending {
		traceIDs = append(traceIDs, traceID)
	}
	a.mu.Unlock()

	// Emit each trace
	for _, traceID := range traceIDs {
		if trace := a.emitTrace(traceID); trace != nil {
			a.dispatchTrace(trace)
		}
	}

	// Also check SQLite for any remaining traces
	if a.storage == nil {
		return
	}
	sqliteStore, ok := a.storage.(*storage.SQLiteStorage)
	if !ok {
		return
	}

	// Get all trace-ids from SQLite (threshold 0 = get all)
	allTraceIDs, err := sqliteStore.GetExpiredPendingTraceIDs(0)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get trace IDs from SQLite during shutdown")
		return
	}

	for _, traceID := range allTraceIDs {
		// Skip if already emitted above
		a.mu.RLock()
		_, inMemory := a.pending[traceID]
		a.mu.RUnlock()
		if inMemory {
			continue // Already handled
		}

		if trace := a.emitTrace(traceID); trace != nil {
			a.dispatchTrace(trace)
		}
	}
}

// AddSpan adds a span to the assembler
func (a *TraceAssembler) AddSpan(span *models.Span) error {
	a.mu.Lock()
	// Note: Cannot use defer here because evictOldestLocked() releases/reacquires the lock
	// We manually unlock at the end

	a.spansReceived++

	traceID := span.TraceID
	spanID := span.SpanID
	now := time.Now()

	// Check if this trace-id is in LRU (seen in last 3 minutes)
	pending, exists := a.pending[traceID]
	if !exists {
		// Check memory limits BEFORE adding new trace-id (prevents exceeding limit)
		for len(a.pending) >= a.maxMemoryTraces {
			// evictOldestLocked releases lock during disk I/O, so we need to re-check after it returns
			a.evictOldestLocked()
		}

		// Re-check after eviction (lock was released/re-acquired)
		pending, exists = a.pending[traceID]
	}

	if !exists {
		// New trace-id: start new 3-minute timer
		pending = &PendingTrace{
			TraceID:     traceID,
			Spans:       make(map[string]*models.Span),
			FirstSeen:   now,
			LastSeen:    now,
			TimerExpiry: now.Add(a.windowDuration), // 3 minutes from now
			Version:     0,
		}
		a.pending[traceID] = pending
	} else if now.After(pending.TimerExpiry) {
		// Timer expired but trace not yet emitted - add span to existing trace
		// The timerLoop will emit it soon. Don't replace the trace or we lose spans.
		// Just add the span and let emission include it.
		// After emission, a new trace can start.
		log.Debug().Str("trace_id", traceID).Msg("Adding span to expired trace pending emission")
	}

	// If trace is being evicted, the span will still be added and Version incremented
	// The eviction logic will detect the version change and preserve the new span

	// Handle duplicates: keep latest span
	pending.Spans[spanID] = span
	pending.LastSeen = now
	pending.Version++ // Track modifications for eviction race detection

	a.mu.Unlock()
	return nil
}

// evictOldestLocked removes the oldest trace-id from memory (LRU eviction)
// Must be called with lock held
func (a *TraceAssembler) evictOldestLocked() {
	if len(a.pending) == 0 {
		return
	}

	var oldestTraceID string
	var oldestTime time.Time = time.Now()

	for traceID, pending := range a.pending {
		// Skip traces that are already being evicted
		if pending.BeingEvicted {
			continue
		}
		if oldestTime.After(pending.FirstSeen) {
			oldestTime = pending.FirstSeen
			oldestTraceID = traceID
		}
	}

	if oldestTraceID == "" {
		return
	}

	pending := a.pending[oldestTraceID]

	// Mark as being evicted to prevent concurrent modifications
	pending.BeingEvicted = true
	versionBeforeEviction := pending.Version

	// Copy spans before releasing lock for disk I/O
	spansCopy := make(map[string]*models.Span, len(pending.Spans))
	for spanID, span := range pending.Spans {
		spansCopy[spanID] = span
	}
	traceIDCopy := oldestTraceID
	firstSeen := pending.FirstSeen

	// Release lock before slow disk I/O
	a.mu.Unlock()
	if a.storage != nil {
		if sqliteStore, ok := a.storage.(*storage.SQLiteStorage); ok {
			for spanID, span := range spansCopy {
				if err := sqliteStore.StorePendingSpan(traceIDCopy, spanID, span); err != nil {
					log.Warn().Err(err).Str("trace_id", traceIDCopy).Str("span_id", spanID).Msg("Failed to write span to disk during eviction")
				}
			}
		}
	}

	// Re-acquire lock to update state
	a.mu.Lock()

	// Check if trace still exists and handle any spans added during eviction
	if currentPending, exists := a.pending[traceIDCopy]; exists {
		if currentPending.Version == versionBeforeEviction {
			// No new spans added during eviction, safe to delete
			delete(a.pending, traceIDCopy)
			log.Debug().Str("trace_id", traceIDCopy).Time("first_seen", firstSeen).Msg("Evicted oldest trace from memory (LRU)")
		} else {
			// New spans were added during eviction - keep in memory but mark as written
			currentPending.BeingEvicted = false
			currentPending.WrittenToDisk = true
			// Remove spans that were already written to disk to save memory
			for spanID := range spansCopy {
				delete(currentPending.Spans, spanID)
			}
			log.Debug().Str("trace_id", traceIDCopy).Int("new_spans", len(currentPending.Spans)).Msg("Kept trace with new spans after eviction")
		}
	}
}

// timerLoop checks for expired timers every second
func (a *TraceAssembler) timerLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.checkExpiredTimers()
		}
	}
}

// checkExpiredTimers finds traces whose timers have expired and emits them
func (a *TraceAssembler) checkExpiredTimers() {
	now := time.Now()
	var expiredTraceIDs []string

	// Check in-memory traces (atomic check and mark for emission)
	a.mu.Lock()
	for traceID, pending := range a.pending {
		if now.After(pending.TimerExpiry) {
			expiredTraceIDs = append(expiredTraceIDs, traceID)
		}
	}
	a.mu.Unlock()

	// Also check SQLite for expired traces (traces that were written to disk)
	// These are traces that were evicted from memory but timer hasn't expired yet
	if a.storage != nil {
		if sqliteStore, ok := a.storage.(*storage.SQLiteStorage); ok {
			sqliteExpired, err := sqliteStore.GetExpiredPendingTraceIDs(a.windowDuration)
			if err == nil {
				// Filter: only include if not already in memory (already handled above)
				a.mu.RLock()
				for _, traceID := range sqliteExpired {
					if _, inMemory := a.pending[traceID]; !inMemory {
						expiredTraceIDs = append(expiredTraceIDs, traceID)
					}
				}
				a.mu.RUnlock()
			}
		}
	}

	// Emit expired traces (deduplicate)
	seen := make(map[string]bool)
	for _, traceID := range expiredTraceIDs {
		if seen[traceID] {
			continue
		}
		seen[traceID] = true

		trace := a.emitTrace(traceID)
		if trace != nil {
			a.dispatchTrace(trace)
		}
	}
}

// batchWriteLoop writes spans to SQLite every minute
func (a *TraceAssembler) batchWriteLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.writePendingToDisk()
		}
	}
}

// writePendingToDisk writes spans older than memoryDuration to SQLite
func (a *TraceAssembler) writePendingToDisk() {
	if a.storage == nil {
		return
	}

	sqliteStore, ok := a.storage.(*storage.SQLiteStorage)
	if !ok {
		return
	}

	cutoff := time.Now().Add(-a.memoryDuration) // Spans older than 1 minute

	// Use local struct to be explicit about what we need
	type traceToWrite struct {
		TraceID string
		SpanIDs []string // Track which spans we're writing
		Spans   map[string]*models.Span
		Version int64
	}

	var tracesToWrite []traceToWrite

	// Collect traces to write (copy spans to avoid holding lock during disk I/O)
	a.mu.Lock()
	for traceID, pending := range a.pending {
		// Skip traces being evicted or already fully written
		if pending.BeingEvicted {
			continue
		}
		if pending.FirstSeen.Before(cutoff) && len(pending.Spans) > 0 {
			// Copy spans map to avoid holding lock during write
			spansCopy := make(map[string]*models.Span, len(pending.Spans))
			spanIDs := make([]string, 0, len(pending.Spans))
			for spanID, span := range pending.Spans {
				spansCopy[spanID] = span
				spanIDs = append(spanIDs, spanID)
			}
			tracesToWrite = append(tracesToWrite, traceToWrite{
				TraceID: traceID,
				SpanIDs: spanIDs,
				Spans:   spansCopy,
				Version: pending.Version,
			})
		}
	}
	a.mu.Unlock()

	// Write spans to disk (without holding lock)
	for _, tw := range tracesToWrite {
		for spanID, span := range tw.Spans {
			if err := sqliteStore.StorePendingSpan(tw.TraceID, spanID, span); err != nil {
				log.Warn().Err(err).Str("trace_id", tw.TraceID).Str("span_id", spanID).Msg("Failed to write span to disk")
			}
		}
		log.Debug().Str("trace_id", tw.TraceID).Int("spans", len(tw.Spans)).Msg("Wrote spans to disk")
	}

	// Mark written spans and optionally clear from memory
	a.mu.Lock()
	for _, tw := range tracesToWrite {
		if pending, exists := a.pending[tw.TraceID]; exists {
			// Only update if no new spans were added during write
			if pending.Version == tw.Version {
				pending.WrittenToDisk = true
				// Clear spans from memory since they're on disk
				pending.Spans = make(map[string]*models.Span)
			} else {
				// New spans added during write - mark as written but keep new spans
				pending.WrittenToDisk = true
				for _, spanID := range tw.SpanIDs {
					delete(pending.Spans, spanID)
				}
			}
		}
	}
	a.mu.Unlock()
}

// lruEvictionLoop evicts trace-ids older than 3 minutes from LRU
func (a *TraceAssembler) lruEvictionLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.evictExpiredFromLRU()
		}
	}
}

// evictExpiredFromLRU removes trace-ids that have been emitted from memory
// Only removes traces where timer expired (should have been emitted by timerLoop)
func (a *TraceAssembler) evictExpiredFromLRU() {
	now := time.Now()
	var expiredTraceIDs []string

	a.mu.Lock()
	for traceID, pending := range a.pending {
		// Only evict if timer expired (trace should have been emitted)
		// Add small buffer (5 seconds) to ensure timerLoop had time to emit
		if now.After(pending.TimerExpiry.Add(5 * time.Second)) {
			expiredTraceIDs = append(expiredTraceIDs, traceID)
		}
	}

	for _, traceID := range expiredTraceIDs {
		delete(a.pending, traceID)
	}
	a.mu.Unlock()

	if len(expiredTraceIDs) > 0 {
		log.Debug().Int("count", len(expiredTraceIDs)).Msg("Evicted expired trace-ids from LRU")
	}

	// Also cleanup old pending spans from SQLite (older than 3 minutes + buffer)
	if a.storage != nil {
		if sqliteStore, ok := a.storage.(*storage.SQLiteStorage); ok {
			cleanupThreshold := a.windowDuration + 5*time.Minute // 3 min window + 5 min buffer
			if err := sqliteStore.CleanupPendingSpans(cleanupThreshold); err != nil {
				log.Warn().Err(err).Msg("Failed to cleanup old pending spans from SQLite")
			}
		}
	}
}

// emitTrace assembles and returns a trace, removing it from pending state
// Returns nil if trace doesn't exist or has no spans
// Unified function that handles both in-memory and storage cases
func (a *TraceAssembler) emitTrace(traceID string) *models.Trace {
	a.mu.Lock()
	pending, exists := a.pending[traceID]

	// If being evicted, skip - the eviction will handle it
	if exists && pending.BeingEvicted {
		a.mu.Unlock()
		return nil
	}

	var memorySpans []*models.Span
	var needStorageLookup bool

	if !exists {
		// Not in memory - need to check storage
		needStorageLookup = true
	} else {
		// Copy spans from memory
		memorySpans = make([]*models.Span, 0, len(pending.Spans))
		for _, span := range pending.Spans {
			memorySpans = append(memorySpans, span)
		}
		needStorageLookup = pending.WrittenToDisk // Also check storage if we wrote there
		// Remove from pending
		delete(a.pending, traceID)
		a.tracesEmitted++
	}
	a.mu.Unlock()

	var spans []*models.Span

	// Merge with storage spans (if any)
	if needStorageLookup && a.storage != nil {
		if sqliteStore, ok := a.storage.(*storage.SQLiteStorage); ok {
			storageSpans, err := sqliteStore.GetPendingSpans(traceID)
			if err == nil && len(storageSpans) > 0 {
				if len(memorySpans) > 0 {
					// Merge: in-memory takes precedence for duplicates
					spanMap := make(map[string]*models.Span)
					for _, span := range storageSpans {
						spanMap[span.SpanID] = span
					}
					for _, span := range memorySpans {
						spanMap[span.SpanID] = span // Overwrite with latest
					}
					spans = make([]*models.Span, 0, len(spanMap))
					for _, span := range spanMap {
						spans = append(spans, span)
					}
				} else {
					spans = storageSpans
				}
			} else {
				spans = memorySpans
			}
			// Cleanup storage
			if err := sqliteStore.DeletePendingSpans(traceID); err != nil {
				log.Warn().Err(err).Str("trace_id", traceID).Msg("Failed to delete pending spans from storage")
			}
		}
	} else {
		spans = memorySpans
	}

	if len(spans) == 0 {
		return nil
	}

	// Build trace
	trace := a.buildTrace(traceID, spans)
	if trace != nil {
		log.Debug().Str("trace_id", traceID).Int("spans", len(spans)).Msg("Emitting trace after 3-minute window")
	}
	return trace
}

func (a *TraceAssembler) dispatchTrace(trace *models.Trace) {
	if trace == nil {
		return
	}
	if a.handler == nil {
		log.Error().Msg("Trace handler is nil")
		return
	}
	if err := a.handler(trace); err != nil {
		log.Error().Err(err).Str("trace_id", trace.TraceID).Msg("Trace handler error")
	}
}

func (a *TraceAssembler) buildTrace(traceID string, spans []*models.Span) *models.Trace {
	if len(spans) == 0 {
		return nil
	}

	var rootSpan *models.Span
	for _, span := range spans {
		if span.IsRoot() {
			rootSpan = span
			break
		}
	}

	// If no root span found, use the span with earliest start time for consistency
	if rootSpan == nil && len(spans) > 0 {
		rootSpan = spans[0]
		for _, span := range spans[1:] {
			if span.StartTime < rootSpan.StartTime {
				rootSpan = span
			}
		}
	}

	var minStart, maxEnd int64 = -1, 0
	var hasError bool
	for _, span := range spans {
		if minStart < 0 || span.StartTime < minStart {
			minStart = span.StartTime
		}
		endTime := span.StartTime + span.Duration
		if endTime > maxEnd {
			maxEnd = endTime
		}
		if span.GetStatus() == "ERROR" {
			hasError = true
		}
	}

	// Handle edge case where all spans have 0 start time
	if minStart < 0 {
		minStart = 0
	}

	serviceID, grouping := a.computeServiceID(rootSpan, spans)

	trace := &models.Trace{
		TraceID:         traceID,
		Spans:           spans,
		RootSpan:        rootSpan,
		ServiceID:       serviceID,
		ServiceGrouping: grouping,
		StartTime:       time.UnixMicro(minStart),
		EndTime:         time.UnixMicro(maxEnd),
		TotalDurationUs: maxEnd - minStart,
		HasError:        hasError,
	}

	return trace
}

func (a *TraceAssembler) computeServiceID(rootSpan *models.Span, spans []*models.Span) (string, models.ServiceGrouping) {
	if rootSpan == nil {
		return "unknown", models.ServiceGrouping{}
	}

	getTag := func(keys ...string) string {
		if val := models.Lookup(rootSpan.Tags, keys); val != "" {
			return val
		}
		for _, span := range spans {
			if span == nil || span == rootSpan {
				continue
			}
			if val := models.Lookup(span.Tags, keys); val != "" {
				return val
			}
		}
		return ""
	}

	k := a.tagKeys
	grouping := models.ServiceGrouping{
		ServiceIdentity: getTag(k.ServiceIdentity...),
		Scope1:          getTag(k.Scope1...),
		Scope2:          getTag(k.Scope2...),
		Scope3:          getTag(k.Scope3...),
		Env:             getTag(k.Env...),
		Operation:       rootSpan.OperationName,
		FeatureGroup:    getTag(k.FeatureGroup...),
		FeatureName:     getTag(k.FeatureName...),
		SubService:      getTag(k.SubService...),
	}
	if grouping.Operation == "" {
		grouping.Operation = getTag("operation", "operation_name")
	}
	if grouping.ServiceIdentity == "" {
		grouping.ServiceIdentity = rootSpan.ServiceName
	}

	parts := make([]string, 0, len(a.identityFields))
	for _, field := range a.identityFields {
		val := ""
		switch field {
		case "service_identity":
			val = grouping.ServiceIdentity
		case "scope1":
			val = grouping.Scope1
		case "scope2":
			val = grouping.Scope2
		case "scope3":
			val = grouping.Scope3
		case "env":
			val = grouping.Env
		case "feature_group":
			val = grouping.FeatureGroup
		case "feature_name":
			val = grouping.FeatureName
		case "sub_service":
			val = grouping.SubService
		case "operation":
			val = grouping.Operation
		case "service_name":
			val = getTag("service_name", "serviceName", "localServiceName")
			if val == "" {
				val = rootSpan.ServiceName
			}
		case "operation_name":
			val = rootSpan.OperationName
		default:
			if v, ok := rootSpan.Tags[field]; ok {
				val = v
			}
		}
		parts = append(parts, val)
	}

	last := len(parts)
	for last > 0 && parts[last-1] == "" {
		last--
	}
	if last == 0 {
		return rootSpan.ServiceName + ":" + rootSpan.OperationName, grouping
	}

	return strings.Join(parts[:last], ":"), grouping
}

// Stats returns current metrics
func (a *TraceAssembler) Stats() (spansReceived, tracesEmitted, pendingTraces int64) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.spansReceived, a.tracesEmitted, int64(len(a.pending))
}
