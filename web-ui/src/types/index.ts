/**
 * Type definitions for the trace-analyzer web-ui
 *
 * These types are designed to match the JSON output from the Go backend.
 *
 * IMPORTANT NOTES:
 * - The Span interface uses PascalCase (TraceID, SpanID, etc.) to match Go's JSON tags
 * - All other interfaces use snake_case to match their respective Go JSON tags
 * - SpanUI is the transformed format returned by the API endpoints (uses snake_case)
 */

// Trace types
export type {
  Span,
  SpanUI,
  TraceSummary,
  TraceDetail,
  TraceBatch,
  QueryComplete,
  InterestingTrace,
  InterestingTraceSummary,
} from './trace';

// Service types
export type {
  ServiceGrouping,
  ServiceIdentity,
  Service,
  ServiceIssueCounts,
  ServiceFilters,
  ServiceBaseline,
  DurationStats,
  BranchBaseline,
} from './service';

// Deviation and Anomaly types
export type {
  Deviation,
  Anomaly,
  TopologyInfo,
  BranchAttribution,
  Rollup,
  BranchStats,
} from './deviation';

// Stats and Variant types
export type {
  Stats,
  BaselineVariant,
  VariantCombination,
  VariantInfo,
  CombinationInfo,
  VariantsResponse,
  CombinationsResponse,
  ActiveVariantResponse,
  SetVariantRequest,
  SetVariantResponse,
  SetCombinationRequest,
  SetCombinationResponse,
  VariantDetailResponse,
  HealthResponse,
  ErrorResponse,
  IngestSpanResponse,
} from './stats';
