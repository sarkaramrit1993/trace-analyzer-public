package models

import (
	"time"
)

// Span represents a single span from the SSE stream (matches actual payload format)
//
// ASSUMPTION: All spans have a valid ParentID linking to another span in the same trace,
// except root spans which have ParentID="". This system assumes synchronous request flows.
// Async flows (via message queues) are treated as separate, unlinked traces.
type Span struct {
	TraceID       string            `json:"TraceID"`
	SpanID        string            `json:"SpanID"`
	ParentID      string            `json:"ParentID"` // empty string for root span
	OperationName string            `json:"OperationName"`
	StartTime     int64             `json:"StartTime"` // unix microseconds
	Duration      int64             `json:"Duration"`  // microseconds
	Tags          map[string]string `json:"Tags"`
	ServiceName   string            `json:"ServiceName"`
}

// IsRoot returns true if this is the root span
func (s *Span) IsRoot() bool {
	return s.ParentID == ""
}

// GetStatus returns status from Tags (http.status_code or error tag)
func (s *Span) GetStatus() string {
	if code, ok := s.Tags["http.status_code"]; ok {
		if code >= "400" {
			return "ERROR"
		}
		return "OK"
	}
	if _, ok := s.Tags["error"]; ok {
		return "ERROR"
	}
	return "OK"
}

// TraceBatch represents the SSE event payload for trace_batch events
type TraceBatch struct {
	Spans []*Span
}

// QueryComplete represents the SSE event for query completion
type QueryComplete struct {
	BytesStreamed int64  `json:"bytesStreamed"`
	JobID         string `json:"jobId"`
	OK            bool   `json:"ok"`
}

// Trace represents an assembled trace with all its spans
type Trace struct {
	TraceID         string
	Spans           []*Span
	RootSpan        *Span
	ServiceID       string // Composite key for grouping
	ServiceGrouping ServiceGrouping
	Fingerprint     string // Topology hash
	StartTime       time.Time
	EndTime         time.Time
	TotalDurationUs int64
	HasError        bool
}

// GetChildren returns child spans of a given span
func (t *Trace) GetChildren(spanID string) []*Span {
	var children []*Span
	for _, s := range t.Spans {
		if s.ParentID == spanID {
			children = append(children, s)
		}
	}
	return children
}

// TraceSummary is a lightweight representation for listings
type TraceSummary struct {
	TraceID     string    `json:"trace_id"`
	ServiceID   string    `json:"service_id"`
	Fingerprint string    `json:"fingerprint"`
	SpanCount   int       `json:"span_count"`
	DurationUs  int64     `json:"duration_us"`
	HasError    bool      `json:"has_error"`
	Timestamp   time.Time `json:"timestamp"`
}

// TopologyInfo describes a unique execution path
type TopologyInfo struct {
	Fingerprint   string  `json:"fingerprint"`
	Count         int64   `json:"count"`
	Percentage    float64 `json:"percentage"`
	AvgDurationUs float64 `json:"avg_duration_us"`
	IsCanonical   bool    `json:"is_canonical"`
}

// ServiceGrouping captures service identity dimensions for grouping
type ServiceGrouping struct {
	ServiceIdentity string `json:"service_identity"`
	Scope1          string `json:"scope1"`
	Scope2          string `json:"scope2"`
	Scope3          string `json:"scope3"`
	Env             string `json:"env"`
	Operation       string `json:"operation"`
	FeatureGroup    string `json:"feature_group"`
	FeatureName     string `json:"feature_name"`
	SubService      string `json:"sub_service"`
}

// ServiceIdentity represents a unique service + operation combination
type ServiceIdentity struct {
	ServiceID     string    `json:"service_id"`
	TraceCount    int64     `json:"trace_count"`
	TopologyCount int       `json:"topology_count"`
	AvgDurationUs float64   `json:"avg_duration_us"`
	ErrorRate     float64   `json:"error_rate"`
	LastSeen      time.Time `json:"last_seen"`
}

// ServiceIssueCounts provides deviation/anomaly counts per service
type ServiceIssueCounts struct {
	DeviationCount int64 `json:"deviation_count"`
	AnomalyCount   int64 `json:"anomaly_count"`
}

// Deviation represents a detected branch deviation
type Deviation struct {
	TraceID              string    `json:"trace_id"`
	ServiceID            string    `json:"service_id"`
	Fingerprint          string    `json:"fingerprint"`
	CanonicalFingerprint string    `json:"canonical_fingerprint"`
	Score                float64   `json:"score"`
	Timestamp            time.Time `json:"timestamp"`
	DiffSummary          string    `json:"diff_summary"`
}

// Anomaly represents a detected performance anomaly
type Anomaly struct {
	TraceID      string    `json:"trace_id"`
	ServiceID    string    `json:"service_id"`
	Fingerprint  string    `json:"fingerprint"`
	Score        float64   `json:"score"`
	SlowBranches []string  `json:"slow_branches"`
	Timestamp    time.Time `json:"timestamp"`
}

// BranchAttribution shows which branch caused slowness
type BranchAttribution struct {
	BranchKey          string  `json:"branch_key"`
	SpanID             string  `json:"span_id"`
	ExpectedDurationUs int64   `json:"expected_duration_us"`
	ActualDurationUs   int64   `json:"actual_duration_us"`
	ExcessDurationUs   int64   `json:"excess_duration_us"`
	AnomalyScore       float64 `json:"anomaly_score"`
	ContributionPct    float64 `json:"contribution_pct"`
}

// Rollup represents aggregated statistics for a time window
type Rollup struct {
	WindowStart     time.Time               `json:"window_start"`
	WindowEnd       time.Time               `json:"window_end"`
	ServiceID       string                  `json:"service_id"`
	ServiceGrouping ServiceGrouping         `json:"service_grouping"`
	Fingerprint     string                  `json:"fingerprint"`
	TraceCount      int64                   `json:"trace_count"`
	SpanCount       int64                   `json:"span_count"`
	ErrorCount      int64                   `json:"error_count"`
	DurationSum     int64                   `json:"duration_sum"`
	DurationMin     int64                   `json:"duration_min"`
	DurationMax     int64                   `json:"duration_max"`
	DurationP50     int64                   `json:"duration_p50"`
	DurationP99     int64                   `json:"duration_p99"`
	BranchStats     map[string]*BranchStats `json:"branch_stats"`
	SampleTraceIDs  []string                `json:"sample_trace_ids"`
}

// BranchStats holds statistics for a single branch
type BranchStats struct {
	Count       int64 `json:"count"`
	SumDuration int64 `json:"sum_duration"`
	MinDuration int64 `json:"min_duration"`
	MaxDuration int64 `json:"max_duration"`
	ErrorCount  int64 `json:"error_count"`
}

// Config holds application configuration
type Config struct {
	SSEEndpoint               string          `json:"sse_endpoint"`
	ServiceIdentityFields     []string        `json:"service_identity_fields"`
	HotRetentionMinutes       int             `json:"hot_retention_minutes"`
	WarmRetentionMinutes      int             `json:"warm_retention_minutes"`
	ColdRetentionHours        int             `json:"cold_retention_hours"`
	DataPath                  string          `json:"data_path"`
	S3Endpoint                string          `json:"s3_endpoint"`
	S3AccessKey               string          `json:"s3_access_key"`
	S3SecretKey               string          `json:"s3_secret_key"`
	S3Bucket                  string          `json:"s3_bucket"`
	S3UseSSL                  bool            `json:"s3_use_ssl"`
	DeviationThreshold        float64         `json:"deviation_threshold"`
	AnomalyZScoreThreshold    float64         `json:"anomaly_zscore_threshold"`
	AnomalyBaselinePercentile float64         `json:"anomaly_baseline_percentile"`
	TraceAssemblyTimeoutSecs  float64         `json:"trace_assembly_timeout_secs"`
	SlidingWindowDuration     string          `json:"sliding_window_duration"` // e.g., "1h", "24h", "168h" (1 week), "720h" (1 month)
	APIPort                   int             `json:"api_port"`
	CleanStart                bool            `json:"clean_start"`
	GroupingTagKeys           GroupingTagKeys `json:"grouping_tag_keys"`
	LearnCombinations         []string        `json:"learn_combinations"` // nil means all 20
	ActiveCombination         string          `json:"active_combination"`
}

// DefaultConfig returns sensible defaults
func DefaultConfig() *Config {
	return &Config{
		SSEEndpoint:               "disabled",
		ServiceIdentityFields:     []string{"service_identity", "scope1", "scope2", "scope3", "env", "feature_group", "feature_name", "sub_service", "operation"},
		HotRetentionMinutes:       15,
		WarmRetentionMinutes:      60,
		ColdRetentionHours:        24,
		DataPath:                  "./data",
		S3Endpoint:                "",
		S3AccessKey:               "",
		S3SecretKey:               "",
		S3Bucket:                  "trace-cold",
		S3UseSSL:                  false,
		DeviationThreshold:        0.3,
		AnomalyZScoreThreshold:    3.5,
		AnomalyBaselinePercentile: 0.75,
		TraceAssemblyTimeoutSecs:  180.0, // 3 minutes (180 seconds)
		SlidingWindowDuration:     "24h", // Default: 1 day (less aggressive than 1 hour)
		APIPort:                   8080,
		CleanStart:                false,
		GroupingTagKeys:           DefaultGroupingTagKeys(),
		ActiveCombination:         "cumulative:50",
	}
}

// GroupingTagKeys lists, per grouping dimension, the span tag keys to read in
// priority order. The first non-empty value wins.
type GroupingTagKeys struct {
	ServiceIdentity []string `json:"service_identity"`
	Scope1          []string `json:"scope1"`
	Scope2          []string `json:"scope2"`
	Scope3          []string `json:"scope3"`
	Env             []string `json:"env"`
	FeatureGroup    []string `json:"feature_group"`
	FeatureName     []string `json:"feature_name"`
	SubService      []string `json:"sub_service"`
}

var defaultGroupingTagKeys = GroupingTagKeys{
	ServiceIdentity: []string{"service_identity", "serviceIdentity", "serviceName", "localServiceName", "service_name", "istio.canonical_service"},
	Scope1:          []string{"scope1", "scope_1", "scope-1"},
	Scope2:          []string{"scope2", "scope_2", "scope-2"},
	Scope3:          []string{"scope3", "scope_3", "scope-3"},
	Env:             []string{"environment", "env"},
	FeatureGroup:    []string{"feature_group", "featureGroup"},
	FeatureName:     []string{"feature_name", "featureName"},
	SubService:      []string{"sub_service", "subService", "subservice"},
}

// DefaultGroupingTagKeys returns the built-in tag keys for each dimension.
// It is called per trace (via NewFingerprinter), so it does not allocate: the
// lists are shared and must be treated as read-only. Their capacity equals
// their length, so append copies instead of writing into the shared array.
func DefaultGroupingTagKeys() GroupingTagKeys {
	d := defaultGroupingTagKeys
	return GroupingTagKeys{
		ServiceIdentity: d.ServiceIdentity[:len(d.ServiceIdentity):len(d.ServiceIdentity)],
		Scope1:          d.Scope1[:len(d.Scope1):len(d.Scope1)],
		Scope2:          d.Scope2[:len(d.Scope2):len(d.Scope2)],
		Scope3:          d.Scope3[:len(d.Scope3):len(d.Scope3)],
		Env:             d.Env[:len(d.Env):len(d.Env)],
		FeatureGroup:    d.FeatureGroup[:len(d.FeatureGroup):len(d.FeatureGroup)],
		FeatureName:     d.FeatureName[:len(d.FeatureName):len(d.FeatureName)],
		SubService:      d.SubService[:len(d.SubService):len(d.SubService)],
	}
}

// WithDefaults returns k with every empty list replaced by its default.
func (k GroupingTagKeys) WithDefaults() GroupingTagKeys {
	d := DefaultGroupingTagKeys()
	pick := func(v, def []string) []string {
		if len(v) == 0 {
			return def
		}
		return v
	}
	return GroupingTagKeys{
		ServiceIdentity: pick(k.ServiceIdentity, d.ServiceIdentity),
		Scope1:          pick(k.Scope1, d.Scope1),
		Scope2:          pick(k.Scope2, d.Scope2),
		Scope3:          pick(k.Scope3, d.Scope3),
		Env:             pick(k.Env, d.Env),
		FeatureGroup:    pick(k.FeatureGroup, d.FeatureGroup),
		FeatureName:     pick(k.FeatureName, d.FeatureName),
		SubService:      pick(k.SubService, d.SubService),
	}
}

// Lookup returns the first non-empty value in tags among keys, or "".
func Lookup(tags map[string]string, keys []string) string {
	for _, key := range keys {
		if val := tags[key]; val != "" {
			return val
		}
	}
	return ""
}
