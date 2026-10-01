/**
 * Deviation, Anomaly, and Topology type definitions
 */

import type { ServiceGrouping } from './service';

/**
 * Deviation represents a detected branch deviation.
 */
export interface Deviation {
  trace_id: string;
  service_id: string;
  service_grouping?: ServiceGrouping;
  fingerprint: string;
  canonical_fingerprint: string;
  score: number;
  diff_summary: string;
  timestamp: string; // ISO 8601 datetime
}

/**
 * Anomaly represents a detected performance anomaly.
 */
export interface Anomaly {
  trace_id: string;
  service_id: string;
  service_grouping?: ServiceGrouping;
  fingerprint: string;
  score: number;
  slow_branches: string[];
  timestamp: string; // ISO 8601 datetime
}

/**
 * TopologyInfo describes a unique execution path (fingerprint).
 */
export interface TopologyInfo {
  fingerprint: string;
  count: number;
  percentage: number;
  avg_duration_us: number;
  is_canonical: boolean;
}

/**
 * BranchAttribution shows which branch caused slowness in an anomaly.
 */
export interface BranchAttribution {
  branch_key: string;
  span_id: string;
  expected_duration_us: number;
  actual_duration_us: number;
  excess_duration_us: number;
  anomaly_score: number;
  contribution_pct: number;
}

/**
 * Rollup represents aggregated statistics for a time window.
 */
export interface Rollup {
  window_start: string; // ISO 8601 datetime
  window_end: string; // ISO 8601 datetime
  service_id: string;
  service_grouping: ServiceGrouping;
  fingerprint: string;
  trace_count: number;
  span_count: number;
  error_count: number;
  duration_sum: number;
  duration_min: number;
  duration_max: number;
  duration_p50: number;
  duration_p99: number;
  branch_stats: Record<string, BranchStats>;
  sample_trace_ids: string[];
}

/**
 * BranchStats holds statistics for a single branch within a rollup.
 */
export interface BranchStats {
  count: number;
  sum_duration: number;
  min_duration: number;
  max_duration: number;
  error_count: number;
}
