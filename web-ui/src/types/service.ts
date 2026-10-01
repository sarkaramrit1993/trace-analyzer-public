/**
 * Service-related type definitions
 */

/**
 * ServiceGrouping captures service identity dimensions for grouping.
 * All fields use snake_case to match Go JSON tags.
 */
export interface ServiceGrouping {
  service_identity: string;
  scope1: string;
  scope2: string;
  scope3: string;
  env: string;
  operation: string;
  feature_group: string;
  feature_name: string;
  sub_service: string;
}

/**
 * ServiceIdentity represents a unique service + operation combination.
 * This is the base model from Go.
 */
export interface ServiceIdentity {
  service_id: string;
  trace_count: number;
  topology_count: number;
  avg_duration_us: number;
  error_rate: number;
  last_seen: string; // ISO 8601 datetime
}

/**
 * Service represents the full service data returned by the API.
 * This extends ServiceIdentity with additional computed fields.
 */
export interface Service {
  service_id: string;
  service_grouping: ServiceGrouping;
  trace_count: number;
  topology_count: number;
  avg_duration_us: number;
  p50_duration_us: number;
  p75_duration_us: number;
  error_rate: number;
  last_seen: string; // ISO 8601 datetime
  deviation_count: number;
  anomaly_count: number;
}

/**
 * ServiceIssueCounts provides deviation/anomaly counts per service.
 */
export interface ServiceIssueCounts {
  deviation_count: number;
  anomaly_count: number;
}

/**
 * ServiceFilters for filtering the services list.
 */
export interface ServiceFilters {
  scope1?: string;
  scope2?: string;
  scope3?: string;
  env?: string;
  service_identity?: string;
}

/**
 * ServiceBaseline represents baseline statistics for a service.
 */
export interface ServiceBaseline {
  service_id: string;
  service_grouping: ServiceGrouping;
  trace_count: number;
  canonical_fingerprint: string;
  duration: DurationStats;
  branches: BranchBaseline[];
}

/**
 * DurationStats holds duration statistics with percentiles.
 */
export interface DurationStats {
  mean: number;
  min: number;
  max: number;
  p50: number;
  p90: number;
  p99: number;
}

/**
 * BranchBaseline holds baseline statistics for a single branch.
 */
export interface BranchBaseline {
  branch_key: string;
  count: number;
  duration: DurationStats;
}
