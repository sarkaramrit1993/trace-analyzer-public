/**
 * Trace-related type definitions
 *
 * IMPORTANT: The Span interface uses PascalCase JSON keys (TraceID, SpanID, etc.)
 * to match the Go backend's JSON tags. All other interfaces use snake_case.
 */

/**
 * Span represents a single span from the trace data.
 *
 * NOTE: This interface uses PascalCase property names to match the Go backend's
 * JSON serialization (json:"TraceID", json:"SpanID", etc.).
 */
export interface Span {
  TraceID: string;
  SpanID: string;
  ParentID: string; // empty string for root span
  OperationName: string;
  StartTime: number; // unix microseconds
  Duration: number; // microseconds
  Tags: Record<string, string>;
  ServiceName: string;
}

/**
 * SpanUI is the UI-friendly span format returned by the API's getTrace endpoint.
 * Uses snake_case to match the API response transformation.
 */
export interface SpanUI {
  span_id: string;
  parent_span_id: string;
  service_name: string;
  operation_name: string;
  start_time: number; // unix microseconds
  duration_us: number; // microseconds
  status: 'OK' | 'ERROR';
  attributes: Record<string, string>;
}

/**
 * TraceSummary is a lightweight representation for trace listings.
 */
export interface TraceSummary {
  trace_id: string;
  service_id: string;
  service_grouping?: ServiceGrouping;
  fingerprint: string;
  span_count: number;
  duration_us: number;
  has_error: boolean;
  timestamp: string; // ISO 8601 datetime
}

/**
 * TraceDetail is the full trace with all spans, returned by getTrace endpoint.
 */
export interface TraceDetail {
  trace_id: string;
  service_id: string;
  service_grouping: ServiceGrouping;
  fingerprint: string;
  total_duration_us: number;
  has_error: boolean;
  spans: SpanUI[];
}

/**
 * TraceBatch represents the SSE event payload for trace_batch events.
 */
export interface TraceBatch {
  Spans: Span[];
}

/**
 * QueryComplete represents the SSE event for query completion.
 */
export interface QueryComplete {
  bytesStreamed: number;
  jobId: string;
  ok: boolean;
}

/**
 * InterestingTrace represents an anomaly/deviation trace retained for reference.
 */
export interface InterestingTrace {
  trace_id: string;
  service_id: string;
  fingerprint: string;
  reason: 'anomaly' | 'deviation' | 'error';
  score: number;
  summary?: string;
  timestamp: string; // ISO 8601 datetime
  spans?: Span[];
}

/**
 * InterestingTraceSummary is a lightweight representation for interesting trace listings.
 */
export interface InterestingTraceSummary {
  trace_id: string;
  service_id: string;
  fingerprint: string;
  reason: 'anomaly' | 'deviation' | 'error';
  score: number;
  summary?: string;
  timestamp: string; // ISO 8601 datetime
  span_count: number;
  duration_us: number;
  has_error: boolean;
}

// Re-export ServiceGrouping for convenience (defined in service.ts)
import type { ServiceGrouping } from './service';
export type { ServiceGrouping };
