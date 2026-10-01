/**
 * Stats and Variant-related type definitions
 */

/**
 * Stats represents the overall system statistics.
 */
export interface Stats {
  active_variant?: string;
  hot_tier_traces: number;
  hot_tier_spans: number;
  total_deviations: number;
  total_anomalies: number;
  total_errors: number;
  interesting_traces: number;
  interesting_traces_pct: number;
  unique_services: number;
  unique_topologies: number;
  uptime_seconds: number;
  service_identity_fields: string[];
}

/**
 * BaselineVariant represents different baseline computation strategies.
 */
export type BaselineVariant =
  | 'cumulative'
  | 'ewma'
  | 'sliding_window'
  | 'exponential_decay'
  | 'same_time_yesterday';

/**
 * VariantCombination represents a variant + threshold combination (e.g., "cumulative:50").
 */
export type VariantCombination = string;

/**
 * VariantInfo contains metadata about a baseline variant.
 */
export interface VariantInfo {
  name: string;
  description: string;
  parameters: Record<string, unknown>;
  min_samples: number;
}

/**
 * CombinationInfo contains metadata about a variant+threshold combination.
 */
export interface CombinationInfo {
  combination: string;
  variant: string;
  threshold: number;
  name: string;
  description: string;
  parameters: Record<string, unknown>;
  min_samples: number;
}

/**
 * VariantsResponse is returned by GET /api/variants.
 */
export interface VariantsResponse {
  variants: VariantInfo[];
  active: string;
}

/**
 * CombinationsResponse is returned by GET /api/variants/combinations.
 */
export interface CombinationsResponse {
  combinations: CombinationInfo[];
  active: string;
}

/**
 * ActiveVariantResponse is returned by GET /api/variants/active.
 */
export interface ActiveVariantResponse {
  variant: string;
  combination: string;
  info: VariantInfo | CombinationInfo;
}

/**
 * SetVariantRequest is the request body for POST /api/variants/active.
 */
export interface SetVariantRequest {
  variant: string;
}

/**
 * SetVariantResponse is returned by POST /api/variants/active.
 */
export interface SetVariantResponse {
  variant: string;
  info: VariantInfo;
  message: string;
}

/**
 * SetCombinationRequest is the request body for POST /api/variants/combination.
 */
export interface SetCombinationRequest {
  combination: string;
}

/**
 * SetCombinationResponse is returned by POST /api/variants/combination.
 */
export interface SetCombinationResponse {
  combination: string;
  info: CombinationInfo;
  message: string;
}

/**
 * VariantDetailResponse is returned by GET /api/variants/:variant. The name in
 * the path may be a bare variant or a "<variant>:<threshold>" combination; the
 * resolved combination is always reported.
 */
export interface VariantDetailResponse {
  variant: string;
  combination: string;
  info: VariantInfo;
  services: number;
}

/**
 * HealthResponse is returned by GET /health.
 */
export interface HealthResponse {
  status: 'healthy' | 'unhealthy';
}

/**
 * ErrorResponse is returned when an API error occurs.
 */
export interface ErrorResponse {
  error: string;
}

/**
 * IngestSpanResponse is returned by POST /api/spans.
 */
export interface IngestSpanResponse {
  ok: boolean;
}
