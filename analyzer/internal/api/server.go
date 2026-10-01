package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/models"
	"github.com/trace-analyzer/internal/storage"
)

// Server provides the REST API
type Server struct {
	router         *gin.Engine
	storage        storage.Storage
	variantManager *analysis.VariantManager
	identityFields []string
	ingestSpan     func(span *models.Span) error
	startTime      time.Time
	activeMu       sync.Mutex // orders switch-then-save so the stored value matches memory
}

// activeCombinationSaver is implemented by stores that keep the active
// combination across restarts.
type activeCombinationSaver interface {
	SaveActiveCombination(combination string) error
}

// NewServerWithVariants creates a new API server with variant manager support
func NewServerWithVariants(store storage.Storage, variantManager *analysis.VariantManager, identityFields []string, ingestSpan func(span *models.Span) error) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	// A service ID embeds the operation name it is grouped by, which is often a
	// real HTTP target such as "GET /". Match routes against the raw, still
	// percent-encoded path so %2F inside :serviceId is not decoded into a path
	// separator, which would redirect to a truncated URL and then 404.
	router.UseRawPath = true
	router.UnescapePathValues = true
	router.Use(gin.Recovery())
	router.Use(corsMiddleware())

	s := &Server{
		router:         router,
		storage:        store,
		variantManager: variantManager,
		identityFields: identityFields,
		ingestSpan:     ingestSpan,
		startTime:      time.Now(),
	}

	s.setupRoutes()
	return s
}

// defaultCORSOrigins are the UI origins this project runs on: the compose
// web-ui (14000) and the vite dev server (3000).
var defaultCORSOrigins = []string{
	"http://localhost:14000",
	"http://127.0.0.1:14000",
	"http://localhost:3000",
	"http://127.0.0.1:3000",
}

// corsMiddleware allows the default local UI origins, or the comma-separated
// list in CORS_ALLOWED_ORIGIN. "*" allows any origin but never with credentials.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		allowed := defaultCORSOrigins
		if env := os.Getenv("CORS_ALLOWED_ORIGIN"); env != "" {
			allowed = nil
			for _, o := range strings.Split(env, ",") {
				if o = strings.TrimSpace(o); o != "" {
					allowed = append(allowed, o)
				}
			}
		}

		c.Writer.Header().Add("Vary", "Origin")
		for _, o := range allowed {
			if o == "*" {
				c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
				break
			}
			if origin != "" && o == origin {
				c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
				c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
				break
			}
		}

		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// sanitizeError returns a generic error message for production to avoid leaking internal details.
// In development mode (when CORS_ALLOWED_ORIGIN is not set or is localhost), it returns the actual error.
func sanitizeError(err error) string {
	origin := os.Getenv("CORS_ALLOWED_ORIGIN")
	// In development (localhost or unset), return actual error for debugging
	if origin == "" || strings.Contains(origin, "localhost") {
		return err.Error()
	}
	// In production, return generic message
	return "An internal error occurred"
}

func (s *Server) setupRoutes() {
	// Health check
	s.router.GET("/health", s.healthCheck)

	// Services API
	s.router.GET("/api/services", s.listServices)
	s.router.GET("/api/services/:serviceId", s.getService)
	s.router.GET("/api/services/:serviceId/topologies", serviceFromPath(s.getTopologies))
	s.router.GET("/api/services/:serviceId/topology-example", s.getTopologyExample)
	s.router.GET("/api/services/:serviceId/deviations", serviceFromPath(s.getDeviations))
	s.router.GET("/api/services/:serviceId/anomalies", serviceFromPath(s.getAnomalies))
	s.router.GET("/api/topologies", serviceFromQuery(s.getTopologies))
	s.router.GET("/api/deviations", serviceFromQuery(s.getDeviations))
	s.router.GET("/api/anomalies", serviceFromQuery(s.getAnomalies))
	s.router.GET("/api/services/:serviceId/baselines", s.getBaselines)

	// Traces API
	s.router.GET("/api/traces/recent", s.getRecentTraces)
	s.router.GET("/api/traces/:traceId", s.getTrace)
	s.router.GET("/api/traces/by-fingerprint", s.getTracesByFingerprint)
	s.router.GET("/api/traces/interesting", s.getInterestingTraces)
	s.router.GET("/api/traces/interesting/:traceId", s.getInterestingTrace)

	// Ingest API
	s.router.POST("/api/spans", s.ingestSpanHandler)
	s.router.POST("/api/spans/batch", s.ingestSpanBatchHandler)

	// Stats API
	s.router.GET("/api/stats", s.getStats)

	// Variant API (A/B testing)
	s.router.GET("/api/variants", s.getVariants)
	s.router.GET("/api/variants/combinations", s.getCombinations)
	s.router.GET("/api/variants/active", s.getActiveVariant)
	s.router.POST("/api/variants/active", s.setActiveVariant)
	s.router.POST("/api/variants/combination", s.setActiveCombination)
	s.router.GET("/api/variants/:variant", s.getVariantInfo)
}

// Handler returns the HTTP handler for testing
func (s *Server) Handler() http.Handler {
	return s.router
}

// serviceFromPath and serviceFromQuery let one handler serve both
// /api/services/:serviceId/X and /api/X?service_id=.
func serviceFromPath(h func(*gin.Context, string)) gin.HandlerFunc {
	return func(c *gin.Context) { h(c, c.Param("serviceId")) }
}

func serviceFromQuery(h func(*gin.Context, string)) gin.HandlerFunc {
	return func(c *gin.Context) {
		serviceID := c.Query("service_id")
		if serviceID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "service_id is required"})
			return
		}
		h(c, serviceID)
	}
}

// ---- Handlers ----

func (s *Server) healthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}

func (s *Server) listServices(c *gin.Context) {
	scope1 := c.Query("scope1")
	scope2 := c.Query("scope2")
	scope3 := c.Query("scope3")
	env := c.Query("env")
	serviceIdentity := c.Query("service_identity")

	// Use active baseline (may change if variant switched)
	baseline := s.getActiveBaseline()

	var issueCounts map[string]models.ServiceIssueCounts
	if provider, ok := s.storage.(storage.ServiceIssueCountsProvider); ok {
		if counts, err := provider.GetServiceIssueCounts(s.activeCombination()); err == nil {
			issueCounts = counts
		}
	}

	services := baseline.GetAllServices()
	resp := make([]gin.H, 0, len(services))
	for _, svc := range services {
		counts := issueCounts[svc.ServiceID]
		// Cache baseline lookup (used for both grouping and percentile calculations)
		var serviceBaseline *analysis.ServiceSummary
		if summary, ok := baseline.GetServiceSummary(svc.ServiceID); ok {
			serviceBaseline = &summary
		}
		grouping := s.serviceGroupingFromBaseline(serviceBaseline)
		if scope1 != "" && grouping["scope1"] != scope1 {
			continue
		}
		if scope2 != "" && grouping["scope2"] != scope2 {
			continue
		}
		if scope3 != "" && grouping["scope3"] != scope3 {
			continue
		}
		if env != "" && grouping["env"] != env {
			continue
		}
		if serviceIdentity != "" && grouping["service_identity"] != serviceIdentity {
			continue
		}
		p50Duration := float64(0)
		p75Duration := float64(0)
		avgDurationFromSamples := float64(0)
		if serviceBaseline != nil {
			// Always calculate percentiles and average from the same dataset for consistency
			// This prevents cases where avg > P75 (which is mathematically impossible for right-skewed latency distributions)
			if serviceBaseline.SampleCount > 0 {
				// Use reservoir samples for both percentiles and average
				p50Duration = serviceBaseline.P50
				p75Duration = serviceBaseline.P75
				avgDurationFromSamples = serviceBaseline.SampleMean
			} else if serviceBaseline.TotalTraces > 0 {
				// Fallback: if no samples yet, calculate from full dataset (but this should be rare)
				// For consistency, we'd need to calculate percentiles from full dataset too
				// For now, use full average and set percentiles to 0 to indicate insufficient data
				avgDurationFromSamples = float64(serviceBaseline.DurationSum) / float64(serviceBaseline.TotalTraces)
				// Percentiles remain 0 when no samples available
			}
		}
		resp = append(resp, gin.H{
			"service_id":       svc.ServiceID,
			"service_grouping": grouping,
			"trace_count":      svc.TraceCount,
			"topology_count":   svc.TopologyCount,
			"avg_duration_us":  avgDurationFromSamples,
			"p50_duration_us":  p50Duration,
			"p75_duration_us":  p75Duration,
			"error_rate":       svc.ErrorRate,
			"last_seen":        svc.LastSeen,
			"deviation_count":  counts.DeviationCount,
			"anomaly_count":    counts.AnomalyCount,
		})
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) getService(c *gin.Context) {
	serviceID := c.Param("serviceId")

	summary, ok := s.getActiveBaseline().GetServiceSummary(serviceID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Service not found"})
		return
	}
	baseline := &summary

	// Always calculate percentiles and average from the same dataset for consistency
	// This prevents cases where avg > P75 (which is mathematically impossible for right-skewed latency distributions)
	avgDuration := float64(0)
	p50Duration := float64(0)
	p75Duration := float64(0)
	if baseline.SampleCount > 0 {
		// Use reservoir samples for both percentiles and average
		p50Duration = baseline.P50
		p75Duration = baseline.P75
		avgDuration = baseline.SampleMean
	} else if baseline.TotalTraces > 0 {
		// Fallback: if no samples yet, calculate from full dataset
		// Note: percentiles remain 0 when no samples available (should be rare)
		avgDuration = float64(baseline.DurationSum) / float64(baseline.TotalTraces)
		// Percentiles would need full dataset calculation for true consistency, but that's expensive
		// For now, set to 0 to indicate insufficient sample data
	}

	errorRate := float64(0)
	if baseline.TotalTraces > 0 {
		errorRate = float64(baseline.ErrorCount) / float64(baseline.TotalTraces)
	}

	var counts models.ServiceIssueCounts
	if provider, ok := s.storage.(storage.ServiceIssueCountsProvider); ok {
		if issueCounts, err := provider.GetServiceIssueCounts(s.activeCombination()); err == nil {
			counts = issueCounts[baseline.ServiceID]
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"service_id":       baseline.ServiceID,
		"service_grouping": s.serviceGroupingFromBaseline(baseline),
		"trace_count":      baseline.TotalTraces,
		"topology_count":   baseline.TopologyCount,
		"avg_duration_us":  avgDuration,
		"p50_duration_us":  p50Duration,
		"p75_duration_us":  p75Duration,
		"error_rate":       errorRate,
		"last_seen":        baseline.LastSeen,
		"deviation_count":  counts.DeviationCount,
		"anomaly_count":    counts.AnomalyCount,
	})
}

func (s *Server) getTopologies(c *gin.Context, serviceID string) {
	topologies := s.getActiveBaseline().GetTopologies(serviceID)
	if topologies == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Service not found"})
		return
	}
	c.JSON(http.StatusOK, topologies)
}

func (s *Server) getTopologyExample(c *gin.Context) {
	serviceID := c.Param("serviceId")
	fingerprint := c.Query("fingerprint")
	if fingerprint == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fingerprint query parameter is required"})
		return
	}

	baseline := s.getActiveBaseline()
	if _, ok := baseline.GetServiceSummary(serviceID); !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Service not found"})
		return
	}

	exampleTrace, exists := baseline.GetTopologyExample(serviceID, fingerprint)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Topology example not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"trace_id":          exampleTrace.TraceID,
		"service_id":        exampleTrace.ServiceID,
		"service_grouping":  s.serviceGrouping(exampleTrace.ServiceID),
		"fingerprint":       exampleTrace.Fingerprint,
		"total_duration_us": exampleTrace.TotalDurationUs,
		"has_error":         exampleTrace.HasError,
		"spans":             spansJSON(exampleTrace.Spans),
	})
}

func (s *Server) getDeviations(c *gin.Context, serviceID string) {
	limit := parseIntParam(c, "limit", 100)
	minScore := parseFloatParam(c, "min_score", 0.0)

	deviations, err := s.storage.GetDeviations(serviceID, s.activeCombination(), limit, minScore)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": sanitizeError(err)})
		return
	}

	resp := make([]gin.H, 0, len(deviations))
	for _, dev := range deviations {
		resp = append(resp, gin.H{
			"trace_id":              dev.TraceID,
			"service_id":            dev.ServiceID,
			"service_grouping":      s.serviceGrouping(dev.ServiceID),
			"fingerprint":           dev.Fingerprint,
			"canonical_fingerprint": dev.CanonicalFingerprint,
			"score":                 dev.Score,
			"diff_summary":          dev.DiffSummary,
			"timestamp":             dev.Timestamp,
		})
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) getAnomalies(c *gin.Context, serviceID string) {
	limit := parseIntParam(c, "limit", 100)
	minScore := parseFloatParam(c, "min_score", 3.5)

	anomalies, err := s.storage.GetAnomalies(serviceID, s.activeCombination(), limit, minScore)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": sanitizeError(err)})
		return
	}

	resp := make([]gin.H, 0, len(anomalies))
	for _, an := range anomalies {
		resp = append(resp, gin.H{
			"trace_id":         an.TraceID,
			"service_id":       an.ServiceID,
			"fingerprint":      an.Fingerprint,
			"service_grouping": s.serviceGrouping(an.ServiceID),
			"score":            an.Score,
			"slow_branches":    an.SlowBranches,
			"timestamp":        an.Timestamp,
		})
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) getTracesByFingerprint(c *gin.Context) {
	serviceID := c.Query("service_id")
	fingerprint := c.Query("fingerprint")
	if serviceID == "" || fingerprint == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service_id and fingerprint query parameters are required"})
		return
	}
	limit := parseIntParam(c, "limit", 50)

	traces, err := s.storage.GetTracesByFingerprint(serviceID, fingerprint, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": sanitizeError(err)})
		return
	}

	resp := make([]gin.H, 0, len(traces))
	for _, t := range traces {
		resp = append(resp, gin.H{
			"trace_id":    t.TraceID,
			"service_id":  t.ServiceID,
			"fingerprint": t.Fingerprint,
			"span_count":  t.SpanCount,
			"duration_us": t.DurationUs,
			"has_error":   t.HasError,
			"timestamp":   t.Timestamp,
		})
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) getInterestingTraces(c *gin.Context) {
	limit := parseIntParam(c, "limit", 50)
	reason := c.Query("reason")

	store, ok := s.storage.(storage.InterestingTraceStore)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "interesting trace storage not available"})
		return
	}

	traces, err := store.GetInterestingTraces(reason, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": sanitizeError(err)})
		return
	}

	c.JSON(http.StatusOK, traces)
}

func (s *Server) getInterestingTrace(c *gin.Context) {
	traceID := c.Param("traceId")
	if traceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "traceId is required"})
		return
	}

	store, ok := s.storage.(storage.InterestingTraceStore)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "interesting trace storage not available"})
		return
	}

	trace, err := store.GetInterestingTrace(traceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": sanitizeError(err)})
		return
	}
	if trace == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "trace not found"})
		return
	}

	c.JSON(http.StatusOK, trace)
}

func (s *Server) getBaselines(c *gin.Context) {
	serviceID := c.Param("serviceId")

	active := s.getActiveBaseline()
	summary, ok := active.GetServiceSummary(serviceID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Service not found"})
		return
	}
	baseline := &summary

	// Build baseline response
	response := gin.H{
		"service_id":            baseline.ServiceID,
		"service_grouping":      s.serviceGrouping(baseline.ServiceID),
		"trace_count":           baseline.TotalTraces,
		"canonical_fingerprint": baseline.CanonicalFingerprint,
		"duration": gin.H{
			"mean": float64(baseline.DurationSum) / float64(max(baseline.TotalTraces, 1)),
			"min":  baseline.DurationMin,
			"max":  baseline.DurationMax,
			"p50":  baseline.P50,
			"p90":  baseline.P90,
			"p99":  baseline.P99,
		},
		"branches": buildBranchBaselines(active.GetBranchSummaries(serviceID)),
	}

	c.JSON(http.StatusOK, response)
}

func buildBranchBaselines(summaries []analysis.BranchSummary) []gin.H {
	var branches []gin.H

	for _, bb := range summaries {
		branches = append(branches, gin.H{
			"branch_key": bb.Key,
			"count":      bb.Count,
			"duration": gin.H{
				"mean": bb.Mean,
				"min":  bb.MinDuration,
				"max":  bb.MaxDuration,
				"p50":  bb.P50,
				"p90":  bb.P90,
				"p99":  bb.P99,
			},
		})
	}

	return branches
}

func (s *Server) getRecentTraces(c *gin.Context) {
	serviceID := c.Query("service_id")
	limit := parseIntParam(c, "limit", 50)

	traces, err := s.storage.GetRecentTraces(serviceID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": sanitizeError(err)})
		return
	}

	resp := make([]gin.H, 0, len(traces))
	for _, tr := range traces {
		resp = append(resp, gin.H{
			"trace_id":         tr.TraceID,
			"service_id":       tr.ServiceID,
			"service_grouping": s.serviceGrouping(tr.ServiceID),
			"fingerprint":      tr.Fingerprint,
			"span_count":       tr.SpanCount,
			"duration_us":      tr.DurationUs,
			"has_error":        tr.HasError,
			"timestamp":        tr.Timestamp,
		})
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) getTrace(c *gin.Context) {
	traceID := c.Param("traceId")

	trace, err := s.storage.GetTrace(traceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": sanitizeError(err)})
		return
	}

	if trace == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trace not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"trace_id":          trace.TraceID,
		"service_id":        trace.ServiceID,
		"service_grouping":  s.serviceGrouping(trace.ServiceID),
		"fingerprint":       trace.Fingerprint,
		"total_duration_us": trace.TotalDurationUs,
		"has_error":         trace.HasError,
		"spans":             spansJSON(trace.Spans),
	})
}

// spansJSON converts spans to the shape the UI's trace view reads.
func spansJSON(spans []*models.Span) []gin.H {
	out := make([]gin.H, len(spans))
	for i, span := range spans {
		out[i] = gin.H{
			"span_id":        span.SpanID,
			"parent_span_id": span.ParentID,
			"service_name":   span.ServiceName,
			"operation_name": span.OperationName,
			"start_time":     span.StartTime,
			"duration_us":    span.Duration,
			"status":         span.GetStatus(),
			"attributes":     span.Tags,
		}
	}
	return out
}

func (s *Server) getStats(c *gin.Context) {
	activeVariant := s.activeCombination()

	// Get stats filtered by variant
	var storageStats storage.StorageStats
	if sqliteStorage, ok := s.storage.(*storage.SQLiteStorage); ok && activeVariant != "" {
		storageStats = sqliteStorage.StatsForVariant(activeVariant)
	} else {
		storageStats = s.storage.Stats()
	}

	services := s.getActiveBaseline().GetAllServices()

	// Count unique topologies across all services
	var totalTopologies int
	for _, svc := range services {
		totalTopologies += svc.TopologyCount
	}

	// Calculate interesting traces percentage (count distinct trace_id to avoid double-counting)
	// A trace can be error + deviation + anomaly, but should only count once
	var distinctInterestingTraces int64
	if sqliteStorage, ok := s.storage.(*storage.SQLiteStorage); ok {
		// Count distinct trace_ids from interesting_traces table
		if count, err := sqliteStorage.GetDistinctInterestingTracesCount(activeVariant); err == nil {
			distinctInterestingTraces = count
		} else {
			// Fallback to sum if query fails
			distinctInterestingTraces = storageStats.DeviationCount + storageStats.AnomalyCount + storageStats.ErrorCount
		}
	} else {
		// Fallback to sum if not SQLiteStorage
		distinctInterestingTraces = storageStats.DeviationCount + storageStats.AnomalyCount + storageStats.ErrorCount
	}

	interestingTracesPercent := float64(0)
	if storageStats.TraceCount > 0 {
		interestingTracesPercent = float64(distinctInterestingTraces) / float64(storageStats.TraceCount) * 100
	}

	c.JSON(http.StatusOK, gin.H{
		"active_variant":          activeVariant,
		"hot_tier_traces":         storageStats.TraceCount,
		"hot_tier_spans":          storageStats.SpanCount,
		"total_deviations":        storageStats.DeviationCount,
		"total_anomalies":         storageStats.AnomalyCount,
		"total_errors":            storageStats.ErrorCount,
		"interesting_traces":      distinctInterestingTraces,
		"interesting_traces_pct":  interestingTracesPercent,
		"unique_services":         len(services),
		"unique_topologies":       totalTopologies,
		"uptime_seconds":          time.Since(s.startTime).Seconds(),
		"service_identity_fields": s.identityFields,
	})
}

func (s *Server) ingestSpanHandler(c *gin.Context) {
	if s.ingestSpan == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "span ingestion not configured"})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSpanBodyBytes)
	var span models.Span
	if err := c.ShouldBindJSON(&span); err != nil {
		bindError(c, err)
		return
	}
	if span.TraceID == "" || span.SpanID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "TraceID and SpanID are required"})
		return
	}

	if err := s.ingestSpan(&span); err != nil {
		log.Error().Err(err).Str("trace_id", span.TraceID).Msg("Failed to ingest span")
	}

	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}

const maxBatchSize = 1000
const maxSpanBodyBytes = 1 << 20
const maxBatchBodyBytes = 10 << 20

// bindError answers 413 when the body hit its size cap, 400 otherwise.
func bindError(c *gin.Context, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeError(err)})
}

func (s *Server) ingestSpanBatchHandler(c *gin.Context) {
	if s.ingestSpan == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "span ingestion not configured"})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBatchBodyBytes)
	var spans []models.Span
	if err := c.ShouldBindJSON(&spans); err != nil {
		bindError(c, err)
		return
	}

	// Handle empty arrays gracefully
	if len(spans) == 0 {
		c.JSON(http.StatusAccepted, gin.H{
			"ok":       true,
			"accepted": 0,
			"rejected": 0,
		})
		return
	}

	// Rate limiting: reject batches that are too large
	if len(spans) > maxBatchSize {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "batch size exceeds maximum of 1000 spans",
		})
		return
	}

	// Validate and count
	accepted := 0
	rejected := 0

	for i := range spans {
		span := &spans[i]
		if span.TraceID == "" || span.SpanID == "" {
			rejected++
			continue
		}
		if err := s.ingestSpan(span); err != nil {
			log.Error().Err(err).Str("trace_id", span.TraceID).Msg("Failed to ingest span")
		}
		accepted++
	}

	c.JSON(http.StatusAccepted, gin.H{
		"ok":       true,
		"accepted": accepted,
		"rejected": rejected,
	})
}

// Helper functions

// activeCombination names the combination whose findings the API shows.
func (s *Server) activeCombination() string {
	return string(s.variantManager.GetActiveCombination())
}

func (s *Server) getActiveBaseline() *analysis.BaselineComputer {
	return s.variantManager.GetActiveBaseline()
}

func parseIntParam(c *gin.Context, name string, defaultVal int) int {
	val := c.Query(name)
	if val == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return i
}

func parseFloatParam(c *gin.Context, name string, defaultVal float64) float64 {
	val := c.Query(name)
	if val == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return defaultVal
	}
	return f
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (s *Server) serviceGrouping(serviceID string) gin.H {
	if serviceID == "" {
		return gin.H{}
	}
	fields := s.identityFields
	if len(fields) == 0 {
		return gin.H{}
	}
	parts := strings.Split(serviceID, ":")
	grouping := gin.H{}
	for i, field := range fields {
		if i < len(parts) {
			grouping[field] = parts[i]
		} else {
			grouping[field] = ""
		}
	}
	if len(parts) > len(fields) {
		grouping["extra"] = parts[len(fields):]
	}
	return grouping
}

func (s *Server) serviceGroupingFromBaseline(baseline *analysis.ServiceSummary) gin.H {
	if baseline == nil {
		return gin.H{}
	}
	grouping := gin.H{
		"service_identity": baseline.ServiceGrouping.ServiceIdentity,
		"scope1":           baseline.ServiceGrouping.Scope1,
		"scope2":           baseline.ServiceGrouping.Scope2,
		"scope3":           baseline.ServiceGrouping.Scope3,
		"env":              baseline.ServiceGrouping.Env,
		"operation":        baseline.ServiceGrouping.Operation,
		"feature_group":    baseline.ServiceGrouping.FeatureGroup,
		"feature_name":     baseline.ServiceGrouping.FeatureName,
		"sub_service":      baseline.ServiceGrouping.SubService,
	}
	hasAny := false
	for _, v := range grouping {
		if v != "" {
			hasAny = true
			break
		}
	}
	if hasAny {
		return grouping
	}
	return s.serviceGrouping(baseline.ServiceID)
}

// Variant API handlers (A/B testing)

func (s *Server) getVariants(c *gin.Context) {
	variants := s.variantManager.GetAllVariants()
	result := make([]gin.H, 0, len(variants))
	for _, variant := range variants {
		info := analysis.GetVariantInfo(variant)
		result = append(result, info)
	}

	c.JSON(http.StatusOK, gin.H{
		"variants": result,
		"active":   string(s.variantManager.GetActiveVariant()),
	})
}

func (s *Server) getCombinations(c *gin.Context) {
	combinations := s.variantManager.GetAllCombinations()
	result := make([]gin.H, 0, len(combinations))
	for _, combo := range combinations {
		info := analysis.GetCombinationInfo(combo)
		if info != nil {
			result = append(result, info)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"combinations": result,
		"active":       string(s.variantManager.GetActiveCombination()),
	})
}

func (s *Server) getActiveVariant(c *gin.Context) {
	activeCombo := s.variantManager.GetActiveCombination()
	info := analysis.GetCombinationInfo(activeCombo)
	if info == nil {
		// Fallback to legacy variant
		active := s.variantManager.GetActiveVariant()
		info = analysis.GetVariantInfo(active)
		c.JSON(http.StatusOK, gin.H{
			"variant":     string(active),
			"combination": string(activeCombo),
			"info":        info,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"variant":     info["variant"],
		"combination": string(activeCombo),
		"info":        info,
	})
}

// switchActive makes combination active and saves it when the store can. A
// failed save is logged; the switch still applies until the next restart.
func (s *Server) switchActive(combination analysis.VariantCombination) bool {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if !s.variantManager.SetActiveCombination(combination) {
		return false
	}
	if saver, ok := s.storage.(activeCombinationSaver); ok {
		if err := saver.SaveActiveCombination(string(combination)); err != nil {
			log.Error().Err(err).Str("combination", string(combination)).Msg("Active combination switched but not saved; restart will not keep it")
		}
	}
	return true
}

func (s *Server) setActiveVariant(c *gin.Context) {
	var req struct {
		Variant string `json:"variant"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeError(err)})
		return
	}

	variant := analysis.BaselineVariant(req.Variant)
	if !s.switchActive(analysis.DefaultCombination(variant)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown variant"})
		return
	}

	info := analysis.GetVariantInfo(variant)
	c.JSON(http.StatusOK, gin.H{
		"variant": string(variant),
		"info":    info,
		"message": "Variant switched successfully",
	})
}

func (s *Server) setActiveCombination(c *gin.Context) {
	var req struct {
		Combination string `json:"combination"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeError(err)})
		return
	}

	combination := analysis.VariantCombination(req.Combination)
	if !s.switchActive(combination) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown combination, or not learned (set LEARN_COMBINATIONS to learn it)"})
		return
	}

	info := analysis.GetCombinationInfo(combination)
	if info == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid combination format"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"combination": string(combination),
		"info":        info,
		"message":     "Combination switched successfully",
	})
}

// getVariantInfo serves GET /api/variants/:name. The name may be a bare variant
// such as "ewma", which resolves to that variant's default combination, or a
// full "<variant>:<threshold>" combination such as "ewma:100".
func (s *Server) getVariantInfo(c *gin.Context) {
	name := c.Param("variant")

	variant := analysis.BaselineVariant(name)
	combination := analysis.DefaultCombination(variant)

	// A name containing ":" is meant as a "<variant>:<threshold>" combination, so
	// report a malformed one as such rather than as an unknown variant.
	if strings.Contains(name, ":") {
		v, threshold, err := analysis.ParseCombination(name)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Invalid combination", "combination": name})
			return
		}
		variant = v
		combination = analysis.VariantCombination(fmt.Sprintf("%s:%d", v, threshold))
	} else if !analysis.IsValidVariant(variant) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown variant", "variant": name})
		return
	}

	info := analysis.GetVariantInfo(variant)

	baseline := s.variantManager.GetCombination(combination)
	if baseline == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Variant not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"variant":     string(variant),
		"combination": string(combination),
		"info":        info,
		"services":    len(baseline.GetAllServices()),
	})
}
