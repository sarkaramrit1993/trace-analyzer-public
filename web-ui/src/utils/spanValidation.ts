/**
 * Span Validation and Sanitization Utilities
 *
 * Provides defensive helpers to handle malformed, incomplete, or invalid span data.
 * These utilities ensure that the UI never crashes due to bad data.
 */

import type { Span, SpanUI } from '../types';

/**
 * Maximum depth for recursive span tree operations.
 * Prevents stack overflow from circular references.
 */
export const MAX_SPAN_DEPTH = 100;

/**
 * Default values for missing span fields
 */
export const DEFAULTS = {
  DURATION: 0,
  SERVICE: 'unknown-service',
  OPERATION: 'unknown-operation',
  STATUS: 'OK' as const,
} as const;

/**
 * Check if a value is a finite non-negative number
 */
export function isValidDuration(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0;
}

/**
 * Check if a value is a finite number (can be negative for timestamps)
 */
export function isValidTimestamp(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}

/**
 * Check if a span has minimum required fields (SpanUI format)
 */
export function isValidSpanUI(span: unknown): span is SpanUI {
  if (!span || typeof span !== 'object') return false;
  const s = span as Record<string, unknown>;
  return typeof s.span_id === 'string' && s.span_id.length > 0;
}

/**
 * Check if a span has minimum required fields (Span format - PascalCase)
 */
export function isValidSpan(span: unknown): span is Span {
  if (!span || typeof span !== 'object') return false;
  const s = span as Record<string, unknown>;
  return typeof s.SpanID === 'string' && s.SpanID.length > 0;
}

/**
 * Sanitize a duration value, returning 0 for invalid values
 */
export function sanitizeDuration(value: unknown): number {
  if (isValidDuration(value)) return value;
  const num = Number(value);
  if (Number.isFinite(num) && num >= 0) return num;
  return DEFAULTS.DURATION;
}

/**
 * Sanitize a timestamp value, returning 0 for invalid values
 */
export function sanitizeTimestamp(value: unknown): number {
  if (isValidTimestamp(value)) return value;
  const num = Number(value);
  if (Number.isFinite(num)) return num;
  return 0;
}

/**
 * Sanitize a status value, returning 'OK' for any non-ERROR value
 */
export function sanitizeStatus(value: unknown): 'OK' | 'ERROR' {
  return value === 'ERROR' ? 'ERROR' : 'OK';
}

/**
 * Sanitize an attributes object, returning empty object for invalid values
 */
export function sanitizeAttributes(value: unknown): Record<string, string> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, string>;
  }
  return {};
}

/**
 * Sanitize a string value, returning fallback for empty/invalid values
 */
export function sanitizeString(value: unknown, fallback: string): string {
  if (typeof value === 'string' && value.length > 0) return value;
  return fallback;
}

/**
 * Sanitize a SpanUI object, providing defaults for missing fields
 */
export function sanitizeSpanUI(span: Partial<SpanUI> | null | undefined): SpanUI {
  if (!span || typeof span !== 'object') {
    return {
      span_id: `unknown-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`,
      parent_span_id: '',
      service_name: DEFAULTS.SERVICE,
      operation_name: DEFAULTS.OPERATION,
      duration_us: DEFAULTS.DURATION,
      start_time: 0,
      status: DEFAULTS.STATUS,
      attributes: {},
    };
  }

  return {
    span_id: sanitizeString(span.span_id, `unknown-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`),
    parent_span_id: typeof span.parent_span_id === 'string' ? span.parent_span_id : '',
    service_name: sanitizeString(span.service_name, DEFAULTS.SERVICE),
    operation_name: sanitizeString(span.operation_name, DEFAULTS.OPERATION),
    duration_us: sanitizeDuration(span.duration_us),
    start_time: sanitizeTimestamp(span.start_time),
    status: sanitizeStatus(span.status),
    attributes: sanitizeAttributes(span.attributes),
  };
}

/**
 * Sanitize a Span object (PascalCase format), providing defaults for missing fields
 */
export function sanitizeSpan(span: Partial<Span> | null | undefined): Span {
  if (!span || typeof span !== 'object') {
    return {
      SpanID: `unknown-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`,
      ParentID: '',
      TraceID: 'unknown',
      ServiceName: DEFAULTS.SERVICE,
      OperationName: DEFAULTS.OPERATION,
      Duration: DEFAULTS.DURATION,
      StartTime: 0,
      Tags: {},
    };
  }

  return {
    SpanID: sanitizeString(span.SpanID, `unknown-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`),
    ParentID: typeof span.ParentID === 'string' ? span.ParentID : '',
    TraceID: sanitizeString(span.TraceID, 'unknown'),
    ServiceName: sanitizeString(span.ServiceName, DEFAULTS.SERVICE),
    OperationName: sanitizeString(span.OperationName, DEFAULTS.OPERATION),
    Duration: sanitizeDuration(span.Duration),
    StartTime: sanitizeTimestamp(span.StartTime),
    Tags: sanitizeAttributes(span.Tags),
  };
}

/**
 * Filter and sanitize an array of SpanUI spans
 * Removes completely invalid entries and sanitizes partial ones
 */
export function sanitizeSpansUI(spans: unknown): SpanUI[] {
  if (!Array.isArray(spans)) return [];

  return spans
    .filter((s): s is Partial<SpanUI> => s != null && typeof s === 'object')
    .map(s => sanitizeSpanUI(s))
    .filter(s => s.span_id && !s.span_id.startsWith('unknown-'));
}

/**
 * Filter and sanitize an array of Span spans (PascalCase)
 * Removes completely invalid entries and sanitizes partial ones
 */
export function sanitizeSpans(spans: unknown): Span[] {
  if (!Array.isArray(spans)) return [];

  return spans
    .filter((s): s is Partial<Span> => s != null && typeof s === 'object')
    .map(s => sanitizeSpan(s))
    .filter(s => s.SpanID && !s.SpanID.startsWith('unknown-'));
}

/**
 * Detect circular references in span parent relationships.
 * Returns set of span IDs that are part of a cycle.
 */
export function detectCircularReferences(spans: SpanUI[]): Set<string> {
  const cycles = new Set<string>();
  const spanMap = new Map(spans.map(s => [s.span_id, s]));

  for (const span of spans) {
    const visited = new Set<string>();
    let current: SpanUI | undefined = span;

    while (current) {
      if (visited.has(current.span_id)) {
        // Found a cycle - add all visited spans to cycles set
        visited.forEach(id => cycles.add(id));
        break;
      }
      visited.add(current.span_id);
      current = current.parent_span_id ? spanMap.get(current.parent_span_id) : undefined;
    }
  }

  return cycles;
}

/**
 * Detect circular references in Span array (PascalCase)
 */
export function detectCircularReferencesSpan(spans: Span[]): Set<string> {
  const cycles = new Set<string>();
  const spanMap = new Map(spans.map(s => [s.SpanID, s]));

  for (const span of spans) {
    const visited = new Set<string>();
    let current: Span | undefined = span;

    while (current) {
      if (visited.has(current.SpanID)) {
        visited.forEach(id => cycles.add(id));
        break;
      }
      visited.add(current.SpanID);
      current = current.ParentID ? spanMap.get(current.ParentID) : undefined;
    }
  }

  return cycles;
}

/**
 * Log a warning about malformed span data (only in development)
 */
export function warnMalformedData(context: string, details: string): void {
  if (!import.meta.env.PROD) {
    console.warn(`[SpanValidation] ${context}: ${details}`);
  }
}
