/**
 * Utility functions for trace visualization
 *
 * These utilities support the TraceWaterfall component by computing
 * span depths, formatting durations, and calculating timing percentages.
 *
 * All functions are hardened against malformed/incomplete span data.
 */

import type { Span, SpanUI } from '../types';
import {
  MAX_SPAN_DEPTH,
  sanitizeDuration,
  sanitizeTimestamp,
  isValidSpan,
  isValidSpanUI,
  warnMalformedData,
} from './spanValidation';

/**
 * Timing information for a span in the waterfall view
 */
export interface SpanTiming {
  /** Percentage offset from trace start (0-100) */
  offsetPercent: number;
  /** Percentage width of the span (0-100) */
  widthPercent: number;
}

/**
 * Computes the nesting depth of a span in the trace tree.
 * Root spans have depth 0, their children have depth 1, etc.
 *
 * Hardened against:
 * - Circular parent references (max depth limit)
 * - Missing parent spans
 * - Invalid span IDs
 *
 * @param span - The span to compute depth for
 * @param spans - All spans in the trace (uses Span type with PascalCase)
 * @returns The nesting level (0 for root spans)
 */
export function computeSpanDepth(span: Span, spans: Span[]): number {
  if (!isValidSpan(span)) return 0;

  // Filter and build map only from valid spans
  const validSpans = spans.filter(isValidSpan);
  const spanMap = new Map(validSpans.map(s => [s.SpanID, s]));

  let depth = 0;
  let currentSpan: Span | undefined = span;
  const visited = new Set<string>();

  while (
    currentSpan?.ParentID &&
    spanMap.has(currentSpan.ParentID) &&
    depth < MAX_SPAN_DEPTH
  ) {
    // Detect circular references
    if (visited.has(currentSpan.SpanID)) {
      warnMalformedData('computeSpanDepth', `Circular reference detected at span ${currentSpan.SpanID}`);
      break;
    }
    visited.add(currentSpan.SpanID);

    depth++;
    currentSpan = spanMap.get(currentSpan.ParentID);
  }

  return depth;
}

/**
 * Computes the nesting depth of a SpanUI in the trace tree.
 * Root spans have depth 0, their children have depth 1, etc.
 *
 * Hardened against:
 * - Circular parent references (max depth limit)
 * - Missing parent spans
 * - Invalid span IDs
 *
 * @param span - The span to compute depth for (uses SpanUI type with snake_case)
 * @param spans - All spans in the trace
 * @returns The nesting level (0 for root spans)
 */
export function computeSpanUIDepth(span: SpanUI, spans: SpanUI[]): number {
  if (!isValidSpanUI(span)) return 0;

  // Filter and build map only from valid spans
  const validSpans = spans.filter(isValidSpanUI);
  const spanMap = new Map(validSpans.map(s => [s.span_id, s]));

  let depth = 0;
  let currentSpan: SpanUI | undefined = span;
  const visited = new Set<string>();

  while (
    currentSpan?.parent_span_id &&
    spanMap.has(currentSpan.parent_span_id) &&
    depth < MAX_SPAN_DEPTH
  ) {
    // Detect circular references
    if (visited.has(currentSpan.span_id)) {
      warnMalformedData('computeSpanUIDepth', `Circular reference detected at span ${currentSpan.span_id}`);
      break;
    }
    visited.add(currentSpan.span_id);

    depth++;
    currentSpan = spanMap.get(currentSpan.parent_span_id);
  }

  return depth;
}

/**
 * Formats a duration in microseconds to a human-readable string.
 *
 * Hardened against:
 * - NaN, undefined, null values
 * - Negative values
 * - Non-numeric values
 *
 * Examples:
 * - 500 -> "0.50ms"
 * - 1500 -> "1.50ms"
 * - 1500000 -> "1.50s"
 * - 90000000 -> "1.50m"
 *
 * @param microseconds - Duration in microseconds
 * @returns Formatted duration string with appropriate unit
 */
export function formatDuration(microseconds: unknown): string {
  const value = sanitizeDuration(microseconds);

  if (value <= 0) {
    return '0.00ms';
  }

  const ms = value / 1000;

  if (ms < 1000) {
    return `${ms.toFixed(2)}ms`;
  }

  const seconds = ms / 1000;
  if (seconds < 60) {
    return `${seconds.toFixed(2)}s`;
  }

  const minutes = seconds / 60;
  return `${minutes.toFixed(2)}m`;
}

/**
 * Computes timing information for all spans in a trace.
 * Returns a map of span ID to timing info (offset and width percentages).
 *
 * Hardened against:
 * - Empty spans array
 * - Invalid span IDs
 * - NaN/undefined StartTime or Duration
 * - Division by zero (zero total duration)
 *
 * The timing is calculated relative to the trace's total time span:
 * - offset = (spanStart - traceStart) / totalDuration * 100
 * - width = spanDuration / totalDuration * 100
 *
 * @param spans - All spans in the trace (uses Span type with PascalCase)
 * @returns Map of span ID to timing information
 */
export function computeSpanTiming(spans: Span[]): Map<string, SpanTiming> {
  const timingMap = new Map<string, SpanTiming>();

  if (!Array.isArray(spans) || spans.length === 0) {
    return timingMap;
  }

  // Filter valid spans and sanitize timing values
  const validSpans = spans.filter(isValidSpan);
  if (validSpans.length === 0) {
    return timingMap;
  }

  // Extract valid timestamps, filtering out NaN/invalid
  const startTimes = validSpans
    .map(s => sanitizeTimestamp(s.StartTime))
    .filter(t => Number.isFinite(t));

  const endTimes = validSpans
    .map(s => sanitizeTimestamp(s.StartTime) + sanitizeDuration(s.Duration))
    .filter(t => Number.isFinite(t));

  if (startTimes.length === 0 || endTimes.length === 0) {
    // All timing values are invalid - distribute evenly
    validSpans.forEach(span => {
      timingMap.set(span.SpanID, {
        offsetPercent: 0,
        widthPercent: 100 / validSpans.length,
      });
    });
    return timingMap;
  }

  // Find trace time boundaries
  const traceStart = Math.min(...startTimes);
  const traceEnd = Math.max(...endTimes);
  const totalDuration = traceEnd - traceStart;

  if (!Number.isFinite(totalDuration) || totalDuration <= 0) {
    // Zero or invalid duration - distribute evenly
    validSpans.forEach(span => {
      timingMap.set(span.SpanID, {
        offsetPercent: 0,
        widthPercent: 100 / validSpans.length,
      });
    });
    return timingMap;
  }

  validSpans.forEach(span => {
    const startTime = sanitizeTimestamp(span.StartTime);
    const duration = sanitizeDuration(span.Duration);

    const offsetPercent = ((startTime - traceStart) / totalDuration) * 100;
    const widthPercent = (duration / totalDuration) * 100;

    // Clamp values to valid percentages
    timingMap.set(span.SpanID, {
      offsetPercent: Math.max(0, Math.min(100, Number.isFinite(offsetPercent) ? offsetPercent : 0)),
      widthPercent: Math.max(0.5, Math.min(100, Number.isFinite(widthPercent) ? widthPercent : 0.5)),
    });
  });

  return timingMap;
}

/**
 * Computes timing information for SpanUI spans.
 *
 * Hardened against:
 * - Empty spans array
 * - Invalid span IDs
 * - NaN/undefined start_time or duration_us
 * - Division by zero (zero total duration)
 *
 * @param spans - All spans in the trace (uses SpanUI type with snake_case)
 * @returns Map of span ID to timing information
 */
export function computeSpanUITiming(spans: SpanUI[]): Map<string, SpanTiming> {
  const timingMap = new Map<string, SpanTiming>();

  if (!Array.isArray(spans) || spans.length === 0) {
    return timingMap;
  }

  // Filter valid spans
  const validSpans = spans.filter(isValidSpanUI);
  if (validSpans.length === 0) {
    return timingMap;
  }

  // Extract valid timestamps, filtering out NaN/invalid
  const startTimes = validSpans
    .map(s => sanitizeTimestamp(s.start_time))
    .filter(t => Number.isFinite(t));

  const endTimes = validSpans
    .map(s => sanitizeTimestamp(s.start_time) + sanitizeDuration(s.duration_us))
    .filter(t => Number.isFinite(t));

  if (startTimes.length === 0 || endTimes.length === 0) {
    // All timing values are invalid - distribute evenly
    validSpans.forEach(span => {
      timingMap.set(span.span_id, {
        offsetPercent: 0,
        widthPercent: 100 / validSpans.length,
      });
    });
    return timingMap;
  }

  // Find trace time boundaries
  const traceStart = Math.min(...startTimes);
  const traceEnd = Math.max(...endTimes);
  const totalDuration = traceEnd - traceStart;

  if (!Number.isFinite(totalDuration) || totalDuration <= 0) {
    // Zero or invalid duration - distribute evenly
    validSpans.forEach(span => {
      timingMap.set(span.span_id, {
        offsetPercent: 0,
        widthPercent: 100 / validSpans.length,
      });
    });
    return timingMap;
  }

  validSpans.forEach(span => {
    const startTime = sanitizeTimestamp(span.start_time);
    const duration = sanitizeDuration(span.duration_us);

    const offsetPercent = ((startTime - traceStart) / totalDuration) * 100;
    const widthPercent = (duration / totalDuration) * 100;

    // Clamp values to valid percentages
    timingMap.set(span.span_id, {
      offsetPercent: Math.max(0, Math.min(100, Number.isFinite(offsetPercent) ? offsetPercent : 0)),
      widthPercent: Math.max(0.5, Math.min(100, Number.isFinite(widthPercent) ? widthPercent : 0.5)),
    });
  });

  return timingMap;
}

/**
 * Calculates the total duration of a trace from its spans.
 *
 * Hardened against:
 * - Empty spans array
 * - Invalid StartTime or Duration values
 * - NaN propagation
 *
 * @param spans - All spans in the trace
 * @returns Total duration in microseconds (0 if no valid spans)
 */
export function getTraceDuration(spans: Span[]): number {
  if (!Array.isArray(spans) || spans.length === 0) return 0;

  const validSpans = spans.filter(isValidSpan);
  if (validSpans.length === 0) return 0;

  // Extract valid timestamps
  const startTimes = validSpans
    .map(s => sanitizeTimestamp(s.StartTime))
    .filter(t => Number.isFinite(t));

  const endTimes = validSpans
    .map(s => sanitizeTimestamp(s.StartTime) + sanitizeDuration(s.Duration))
    .filter(t => Number.isFinite(t));

  if (startTimes.length === 0 || endTimes.length === 0) return 0;

  const traceStart = Math.min(...startTimes);
  const traceEnd = Math.max(...endTimes);
  const duration = traceEnd - traceStart;

  return Number.isFinite(duration) && duration > 0 ? duration : 0;
}

/**
 * Calculates the total duration of a trace from SpanUI spans.
 *
 * Hardened against:
 * - Empty spans array
 * - Invalid start_time or duration_us values
 * - NaN propagation
 *
 * @param spans - All spans in the trace
 * @returns Total duration in microseconds (0 if no valid spans)
 */
export function getTraceDurationFromUI(spans: SpanUI[]): number {
  if (!Array.isArray(spans) || spans.length === 0) return 0;

  const validSpans = spans.filter(isValidSpanUI);
  if (validSpans.length === 0) return 0;

  // Extract valid timestamps
  const startTimes = validSpans
    .map(s => sanitizeTimestamp(s.start_time))
    .filter(t => Number.isFinite(t));

  const endTimes = validSpans
    .map(s => sanitizeTimestamp(s.start_time) + sanitizeDuration(s.duration_us))
    .filter(t => Number.isFinite(t));

  if (startTimes.length === 0 || endTimes.length === 0) return 0;

  const traceStart = Math.min(...startTimes);
  const traceEnd = Math.max(...endTimes);
  const duration = traceEnd - traceStart;

  return Number.isFinite(duration) && duration > 0 ? duration : 0;
}
