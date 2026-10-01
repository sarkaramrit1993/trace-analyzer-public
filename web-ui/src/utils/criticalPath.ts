/**
 * Critical Path Detection for Trace Visualization
 *
 * The critical path is the longest path through a trace tree, representing
 * the sequence of spans that determines the total trace duration. Highlighting
 * the critical path helps users identify bottlenecks.
 *
 * Algorithm:
 * 1. Build a parent-children map from the spans
 * 2. Starting from root spans, recursively find the longest path
 * 3. Return the set of span IDs on that path
 *
 * All functions are hardened against:
 * - Circular parent references (max depth limit + visited tracking)
 * - Invalid/missing span IDs
 * - NaN/invalid duration values
 */

import type { Span, SpanUI } from '../types';
import {
  MAX_SPAN_DEPTH,
  sanitizeDuration,
  isValidSpan,
  isValidSpanUI,
  warnMalformedData,
} from './spanValidation';

/**
 * Result of finding the longest path from a span
 */
interface LongestPathResult {
  /** Total duration along this path in microseconds */
  duration: number;
  /** Array of span IDs on this path, from ancestor to descendant */
  path: string[];
}

/**
 * Computes the critical path through a trace.
 *
 * The critical path is defined as the longest path (by total duration) through
 * the span tree from any root to any leaf. This represents the sequence of
 * operations that determines the total trace duration.
 *
 * Hardened against:
 * - Circular parent references (visited set + max depth)
 * - Invalid span IDs (filtered out)
 * - NaN/negative duration values (sanitized to 0)
 *
 * @param spans - All spans in the trace (uses Span type with PascalCase: SpanID, ParentID, Duration)
 * @returns Set of span IDs that are on the critical path
 *
 * @example
 * ```typescript
 * const spans: Span[] = [
 *   { SpanID: 'root', ParentID: '', Duration: 100, ... },
 *   { SpanID: 'child1', ParentID: 'root', Duration: 50, ... },
 *   { SpanID: 'child2', ParentID: 'root', Duration: 80, ... },
 * ];
 * const criticalPath = computeCriticalPath(spans);
 * // Returns Set containing 'root' and 'child2' (longest path)
 * ```
 */
export function computeCriticalPath(spans: Span[]): Set<string> {
  if (!Array.isArray(spans) || spans.length === 0) {
    return new Set();
  }

  // Filter to only valid spans
  const validSpans = spans.filter(isValidSpan);
  if (validSpans.length === 0) {
    return new Set();
  }

  // Build a map of span ID to span for quick lookup
  const spanMap = new Map<string, Span>(validSpans.map(s => [s.SpanID, s]));

  // Build a map of parent ID to children spans
  const childrenMap = new Map<string, Span[]>();

  validSpans.forEach(span => {
    if (span.ParentID && typeof span.ParentID === 'string') {
      const siblings = childrenMap.get(span.ParentID) || [];
      siblings.push(span);
      childrenMap.set(span.ParentID, siblings);
    }
  });

  /**
   * Recursively finds the longest path starting from a given span.
   * The path includes the starting span and all descendants along the longest path.
   *
   * @param spanId - The span ID to start from
   * @param visited - Set of already visited span IDs (cycle detection)
   * @param depth - Current recursion depth
   */
  function findLongestPath(
    spanId: string,
    visited: Set<string>,
    depth: number
  ): LongestPathResult {
    // Guard against cycles and excessive depth
    if (visited.has(spanId)) {
      warnMalformedData('computeCriticalPath', `Circular reference detected at span ${spanId}`);
      return { duration: 0, path: [] };
    }

    if (depth > MAX_SPAN_DEPTH) {
      warnMalformedData('computeCriticalPath', `Max depth exceeded at span ${spanId}`);
      return { duration: 0, path: [] };
    }

    const span = spanMap.get(spanId);
    if (!span) {
      return { duration: 0, path: [] };
    }

    // Sanitize duration to prevent NaN propagation
    const duration = sanitizeDuration(span.Duration);

    const childSpans = childrenMap.get(spanId) || [];

    // Leaf node - just this span
    if (childSpans.length === 0) {
      return { duration, path: [spanId] };
    }

    // Mark as visited before recursing
    const newVisited = new Set(visited);
    newVisited.add(spanId);

    // Find the longest child path
    let longestChildPath: LongestPathResult | null = null;

    for (const child of childSpans) {
      if (!child.SpanID) continue;

      const childResult = findLongestPath(child.SpanID, newVisited, depth + 1);

      // Use sanitized comparison to avoid NaN issues
      const childDuration = Number.isFinite(childResult.duration) ? childResult.duration : 0;
      const longestDuration = longestChildPath
        ? (Number.isFinite(longestChildPath.duration) ? longestChildPath.duration : 0)
        : -1;

      if (childDuration > longestDuration) {
        longestChildPath = childResult;
      }
    }

    // Return this span + longest child path
    const childDuration = longestChildPath?.duration ?? 0;
    return {
      duration: duration + (Number.isFinite(childDuration) ? childDuration : 0),
      path: [spanId, ...(longestChildPath?.path ?? [])],
    };
  }

  // Find root spans (no parent or parent not in trace)
  const roots = validSpans.filter(s => !s.ParentID || !spanMap.has(s.ParentID));

  // If no roots found (all spans have parents not in trace), treat all as potential roots
  const startingSpans = roots.length > 0 ? roots : validSpans;

  // Find the globally longest path starting from any root
  let globalLongest: LongestPathResult | null = null;

  for (const root of startingSpans) {
    if (!root.SpanID) continue;

    const result = findLongestPath(root.SpanID, new Set(), 0);

    const resultDuration = Number.isFinite(result.duration) ? result.duration : 0;
    const globalDuration = globalLongest
      ? (Number.isFinite(globalLongest.duration) ? globalLongest.duration : 0)
      : -1;

    if (resultDuration > globalDuration) {
      globalLongest = result;
    }
  }

  return new Set(globalLongest?.path ?? []);
}

/**
 * Computes the critical path through a trace using SpanUI types.
 *
 * This is a variant of computeCriticalPath that works with the SpanUI type
 * which uses snake_case property names (span_id, parent_span_id, duration_us).
 *
 * Hardened against:
 * - Circular parent references (visited set + max depth)
 * - Invalid span IDs (filtered out)
 * - NaN/negative duration values (sanitized to 0)
 *
 * @param spans - All spans in the trace (uses SpanUI type with snake_case)
 * @returns Set of span IDs that are on the critical path
 */
export function computeCriticalPathUI(spans: SpanUI[]): Set<string> {
  if (!Array.isArray(spans) || spans.length === 0) {
    return new Set();
  }

  // Filter to only valid spans
  const validSpans = spans.filter(isValidSpanUI);
  if (validSpans.length === 0) {
    return new Set();
  }

  // Build a map of span ID to span for quick lookup
  const spanMap = new Map<string, SpanUI>(validSpans.map(s => [s.span_id, s]));

  // Build a map of parent ID to children spans
  const childrenMap = new Map<string, SpanUI[]>();

  validSpans.forEach(span => {
    if (span.parent_span_id && typeof span.parent_span_id === 'string') {
      const siblings = childrenMap.get(span.parent_span_id) || [];
      siblings.push(span);
      childrenMap.set(span.parent_span_id, siblings);
    }
  });

  /**
   * Recursively finds the longest path starting from a given span.
   *
   * @param spanId - The span ID to start from
   * @param visited - Set of already visited span IDs (cycle detection)
   * @param depth - Current recursion depth
   */
  function findLongestPath(
    spanId: string,
    visited: Set<string>,
    depth: number
  ): LongestPathResult {
    // Guard against cycles and excessive depth
    if (visited.has(spanId)) {
      warnMalformedData('computeCriticalPathUI', `Circular reference detected at span ${spanId}`);
      return { duration: 0, path: [] };
    }

    if (depth > MAX_SPAN_DEPTH) {
      warnMalformedData('computeCriticalPathUI', `Max depth exceeded at span ${spanId}`);
      return { duration: 0, path: [] };
    }

    const span = spanMap.get(spanId);
    if (!span) {
      return { duration: 0, path: [] };
    }

    // Sanitize duration to prevent NaN propagation
    const duration = sanitizeDuration(span.duration_us);

    const childSpans = childrenMap.get(spanId) || [];

    // Leaf node - just this span
    if (childSpans.length === 0) {
      return { duration, path: [spanId] };
    }

    // Mark as visited before recursing
    const newVisited = new Set(visited);
    newVisited.add(spanId);

    // Find the longest child path
    let longestChildPath: LongestPathResult | null = null;

    for (const child of childSpans) {
      if (!child.span_id) continue;

      const childResult = findLongestPath(child.span_id, newVisited, depth + 1);

      // Use sanitized comparison to avoid NaN issues
      const childDuration = Number.isFinite(childResult.duration) ? childResult.duration : 0;
      const longestDuration = longestChildPath
        ? (Number.isFinite(longestChildPath.duration) ? longestChildPath.duration : 0)
        : -1;

      if (childDuration > longestDuration) {
        longestChildPath = childResult;
      }
    }

    // Return this span + longest child path
    const childDuration = longestChildPath?.duration ?? 0;
    return {
      duration: duration + (Number.isFinite(childDuration) ? childDuration : 0),
      path: [spanId, ...(longestChildPath?.path ?? [])],
    };
  }

  // Find root spans (no parent or parent not in trace)
  const roots = validSpans.filter(s => !s.parent_span_id || !spanMap.has(s.parent_span_id));

  // If no roots found (all spans have parents not in trace), treat all as potential roots
  const startingSpans = roots.length > 0 ? roots : validSpans;

  // Find the globally longest path starting from any root
  let globalLongest: LongestPathResult | null = null;

  for (const root of startingSpans) {
    if (!root.span_id) continue;

    const result = findLongestPath(root.span_id, new Set(), 0);

    const resultDuration = Number.isFinite(result.duration) ? result.duration : 0;
    const globalDuration = globalLongest
      ? (Number.isFinite(globalLongest.duration) ? globalLongest.duration : 0)
      : -1;

    if (resultDuration > globalDuration) {
      globalLongest = result;
    }
  }

  return new Set(globalLongest?.path ?? []);
}
