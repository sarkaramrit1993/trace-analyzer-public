package models

import "time"

// InterestingTrace represents an anomaly/deviation trace retained for reference.
type InterestingTrace struct {
	TraceID     string    `json:"trace_id"`
	ServiceID   string    `json:"service_id"`
	Fingerprint string    `json:"fingerprint"`
	Reason      string    `json:"reason"` // "anomaly" or "deviation"
	Score       float64   `json:"score"`
	Summary     string    `json:"summary,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	Spans       []*Span   `json:"spans,omitempty"`
}

// InterestingTraceSummary is a lightweight representation for listings.
type InterestingTraceSummary struct {
	TraceID     string    `json:"trace_id"`
	ServiceID   string    `json:"service_id"`
	Fingerprint string    `json:"fingerprint"`
	Reason      string    `json:"reason"`
	Score       float64   `json:"score"`
	Summary     string    `json:"summary,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	SpanCount   int       `json:"span_count"`
	DurationUs  int64     `json:"duration_us"`
	HasError    bool      `json:"has_error"`
}
