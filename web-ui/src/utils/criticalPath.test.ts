import { describe, it, expect } from 'vitest';
import { computeCriticalPath, computeCriticalPathUI } from './criticalPath';
import type { Span, SpanUI } from '../types';

/**
 * Helper to create a minimal Span for testing
 */
function createSpan(overrides: Partial<Span>): Span {
  return {
    TraceID: 'trace-1',
    SpanID: 'span-1',
    ParentID: '',
    OperationName: 'test-op',
    StartTime: 1000000,
    Duration: 10000,
    Tags: {},
    ServiceName: 'test-service',
    ...overrides,
  };
}

/**
 * Helper to create a minimal SpanUI for testing
 */
function createSpanUI(overrides: Partial<SpanUI>): SpanUI {
  return {
    span_id: 'span-1',
    parent_span_id: '',
    service_name: 'test-service',
    operation_name: 'test-op',
    start_time: 1000000,
    duration_us: 10000,
    status: 'OK',
    attributes: {},
    ...overrides,
  };
}

describe('computeCriticalPath', () => {
  describe('empty and single span cases', () => {
    it('returns empty set for empty spans array', () => {
      const result = computeCriticalPath([]);
      expect(result.size).toBe(0);
    });

    it('returns single span as critical path for single span trace', () => {
      const span = createSpan({ SpanID: 'only-span', Duration: 100 });
      const result = computeCriticalPath([span]);

      expect(result.size).toBe(1);
      expect(result.has('only-span')).toBe(true);
    });
  });

  describe('linear trace (no branching)', () => {
    it('identifies all spans in linear trace as critical path', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: 100 }),
        createSpan({ SpanID: 'child-1', ParentID: 'root', Duration: 200 }),
        createSpan({ SpanID: 'child-2', ParentID: 'child-1', Duration: 150 }),
      ];

      const result = computeCriticalPath(spans);

      expect(result.size).toBe(3);
      expect(result.has('root')).toBe(true);
      expect(result.has('child-1')).toBe(true);
      expect(result.has('child-2')).toBe(true);
    });
  });

  describe('branching trace', () => {
    it('selects longer branch as critical path', () => {
      // Tree structure:
      //       root (100)
      //      /    \
      //  short(50) long(200)
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: 100 }),
        createSpan({ SpanID: 'short', ParentID: 'root', Duration: 50 }),
        createSpan({ SpanID: 'long', ParentID: 'root', Duration: 200 }),
      ];

      const result = computeCriticalPath(spans);

      expect(result.size).toBe(2);
      expect(result.has('root')).toBe(true);
      expect(result.has('long')).toBe(true);
      expect(result.has('short')).toBe(false);
    });

    it('handles multiple levels of branching', () => {
      // Tree structure:
      //           root (100)
      //          /         \
      //      left(50)    right(80)
      //      /    \          |
      //   ll(10) lr(30)   rr(200)
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: 100 }),
        createSpan({ SpanID: 'left', ParentID: 'root', Duration: 50 }),
        createSpan({ SpanID: 'right', ParentID: 'root', Duration: 80 }),
        createSpan({ SpanID: 'll', ParentID: 'left', Duration: 10 }),
        createSpan({ SpanID: 'lr', ParentID: 'left', Duration: 30 }),
        createSpan({ SpanID: 'rr', ParentID: 'right', Duration: 200 }),
      ];

      // Critical path: root(100) -> right(80) -> rr(200) = 380
      // vs root(100) -> left(50) -> lr(30) = 180
      const result = computeCriticalPath(spans);

      expect(result.size).toBe(3);
      expect(result.has('root')).toBe(true);
      expect(result.has('right')).toBe(true);
      expect(result.has('rr')).toBe(true);
      expect(result.has('left')).toBe(false);
      expect(result.has('ll')).toBe(false);
      expect(result.has('lr')).toBe(false);
    });

    it('handles equal duration branches (selects first found)', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: 100 }),
        createSpan({ SpanID: 'branch-a', ParentID: 'root', Duration: 50 }),
        createSpan({ SpanID: 'branch-b', ParentID: 'root', Duration: 50 }),
      ];

      const result = computeCriticalPath(spans);

      expect(result.size).toBe(2);
      expect(result.has('root')).toBe(true);
      // One of the branches should be selected (deterministic based on iteration order)
      expect(result.has('branch-a') || result.has('branch-b')).toBe(true);
    });
  });

  describe('multiple root spans', () => {
    it('selects longest path among multiple roots', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root-1', ParentID: '', Duration: 100 }),
        createSpan({ SpanID: 'root-2', ParentID: '', Duration: 300 }),
        createSpan({ SpanID: 'child-1', ParentID: 'root-1', Duration: 50 }),
      ];

      const result = computeCriticalPath(spans);

      expect(result.size).toBe(1);
      expect(result.has('root-2')).toBe(true);
      expect(result.has('root-1')).toBe(false);
    });

    it('handles roots with deeper children', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root-1', ParentID: '', Duration: 100 }),
        createSpan({ SpanID: 'root-2', ParentID: '', Duration: 50 }),
        createSpan({ SpanID: 'child-of-1', ParentID: 'root-1', Duration: 30 }),
        createSpan({ SpanID: 'child-of-2', ParentID: 'root-2', Duration: 200 }),
      ];

      // root-1 path: 100 + 30 = 130
      // root-2 path: 50 + 200 = 250
      const result = computeCriticalPath(spans);

      expect(result.size).toBe(2);
      expect(result.has('root-2')).toBe(true);
      expect(result.has('child-of-2')).toBe(true);
    });
  });

  describe('orphan spans', () => {
    it('treats orphan spans as roots', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'orphan', ParentID: 'non-existent', Duration: 500 }),
        createSpan({ SpanID: 'root', ParentID: '', Duration: 100 }),
      ];

      const result = computeCriticalPath(spans);

      expect(result.size).toBe(1);
      expect(result.has('orphan')).toBe(true);
    });
  });

  describe('deeply nested traces', () => {
    it('handles deeply nested spans correctly', () => {
      const spans: Span[] = [];
      const depth = 10;

      for (let i = 0; i < depth; i++) {
        spans.push(
          createSpan({
            SpanID: `level-${i}`,
            ParentID: i === 0 ? '' : `level-${i - 1}`,
            Duration: 10 * (i + 1),
          })
        );
      }

      const result = computeCriticalPath(spans);

      expect(result.size).toBe(depth);
      for (let i = 0; i < depth; i++) {
        expect(result.has(`level-${i}`)).toBe(true);
      }
    });
  });

  describe('wide traces (many siblings)', () => {
    it('handles many sibling spans correctly', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: 100 }),
      ];

      // Add 20 children with varying durations
      for (let i = 0; i < 20; i++) {
        spans.push(
          createSpan({
            SpanID: `child-${i}`,
            ParentID: 'root',
            Duration: i === 15 ? 1000 : 10, // child-15 is the longest
          })
        );
      }

      const result = computeCriticalPath(spans);

      expect(result.size).toBe(2);
      expect(result.has('root')).toBe(true);
      expect(result.has('child-15')).toBe(true);
    });
  });

  describe('zero duration spans', () => {
    it('handles spans with zero duration', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: 0 }),
        createSpan({ SpanID: 'child', ParentID: 'root', Duration: 100 }),
      ];

      const result = computeCriticalPath(spans);

      expect(result.size).toBe(2);
      expect(result.has('root')).toBe(true);
      expect(result.has('child')).toBe(true);
    });

    it('handles all spans with zero duration', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: 0 }),
        createSpan({ SpanID: 'child-a', ParentID: 'root', Duration: 0 }),
        createSpan({ SpanID: 'child-b', ParentID: 'root', Duration: 0 }),
      ];

      const result = computeCriticalPath(spans);

      // With zero durations, the algorithm still picks a valid path
      // Root + one of the children (first found with equal duration)
      expect(result.size).toBe(2);
      expect(result.has('root')).toBe(true);
      expect(result.has('child-a') || result.has('child-b')).toBe(true);
    });
  });
});

describe('computeCriticalPathUI', () => {
  describe('basic functionality (snake_case properties)', () => {
    it('returns empty set for empty spans array', () => {
      const result = computeCriticalPathUI([]);
      expect(result.size).toBe(0);
    });

    it('identifies critical path with snake_case properties', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'root', parent_span_id: '', duration_us: 100 }),
        createSpanUI({ span_id: 'short', parent_span_id: 'root', duration_us: 50 }),
        createSpanUI({ span_id: 'long', parent_span_id: 'root', duration_us: 200 }),
      ];

      const result = computeCriticalPathUI(spans);

      expect(result.size).toBe(2);
      expect(result.has('root')).toBe(true);
      expect(result.has('long')).toBe(true);
      expect(result.has('short')).toBe(false);
    });

    it('handles single span trace', () => {
      const span = createSpanUI({ span_id: 'only-span', duration_us: 100 });
      const result = computeCriticalPathUI([span]);

      expect(result.size).toBe(1);
      expect(result.has('only-span')).toBe(true);
    });

    it('handles linear trace correctly', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'root', parent_span_id: '', duration_us: 100 }),
        createSpanUI({ span_id: 'child', parent_span_id: 'root', duration_us: 200 }),
        createSpanUI({ span_id: 'grandchild', parent_span_id: 'child', duration_us: 150 }),
      ];

      const result = computeCriticalPathUI(spans);

      expect(result.size).toBe(3);
      expect(result.has('root')).toBe(true);
      expect(result.has('child')).toBe(true);
      expect(result.has('grandchild')).toBe(true);
    });

    it('handles orphan spans (parent not in trace)', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'orphan', parent_span_id: 'missing-parent', duration_us: 500 }),
        createSpanUI({ span_id: 'root', parent_span_id: '', duration_us: 100 }),
      ];

      const result = computeCriticalPathUI(spans);

      expect(result.size).toBe(1);
      expect(result.has('orphan')).toBe(true);
    });
  });

  describe('realistic trace scenarios', () => {
    it('identifies critical path in a typical API call trace', () => {
      // Simulates: API Gateway -> Auth Service + User Service + Cache Service
      // Where User Service makes a slow DB call
      const spans: SpanUI[] = [
        createSpanUI({
          span_id: 'gateway',
          parent_span_id: '',
          service_name: 'api-gateway',
          operation_name: 'handleRequest',
          duration_us: 150000, // 150ms total
        }),
        createSpanUI({
          span_id: 'auth',
          parent_span_id: 'gateway',
          service_name: 'auth-service',
          operation_name: 'validateToken',
          duration_us: 5000, // 5ms
        }),
        createSpanUI({
          span_id: 'user',
          parent_span_id: 'gateway',
          service_name: 'user-service',
          operation_name: 'getUser',
          duration_us: 80000, // 80ms
        }),
        createSpanUI({
          span_id: 'user-db',
          parent_span_id: 'user',
          service_name: 'postgres',
          operation_name: 'SELECT',
          duration_us: 50000, // 50ms (slow query)
        }),
        createSpanUI({
          span_id: 'cache',
          parent_span_id: 'gateway',
          service_name: 'redis',
          operation_name: 'GET',
          duration_us: 2000, // 2ms
        }),
      ];

      // Critical path: gateway(150000) -> user(80000) -> user-db(50000) = 280000
      // vs gateway -> auth = 155000
      // vs gateway -> cache = 152000
      const result = computeCriticalPathUI(spans);

      expect(result.size).toBe(3);
      expect(result.has('gateway')).toBe(true);
      expect(result.has('user')).toBe(true);
      expect(result.has('user-db')).toBe(true);
      expect(result.has('auth')).toBe(false);
      expect(result.has('cache')).toBe(false);
    });
  });
});

describe('Robustness and Edge Cases', () => {
  describe('circular references', () => {
    it('handles direct self-reference (span A has parent_span_id = A)', () => {
      // Span points to itself as parent
      const spans: Span[] = [
        createSpan({ SpanID: 'self-ref', ParentID: 'self-ref', Duration: 100 }),
        createSpan({ SpanID: 'normal', ParentID: '', Duration: 50 }),
      ];

      // Should not throw, should handle gracefully
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      // Should select the normal span as it's a valid root
      expect(result.has('normal')).toBe(true);
    });

    it('handles two-node cycle (A->B->A)', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'A', ParentID: 'B', Duration: 100 }),
        createSpan({ SpanID: 'B', ParentID: 'A', Duration: 200 }),
        createSpan({ SpanID: 'C', ParentID: '', Duration: 50 }),
      ];

      // Should not throw, should handle gracefully
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      // A and B form a cycle, so they're treated as orphans (roots)
      // One of them or C should be in the critical path
      expect(result.size).toBeGreaterThan(0);
    });

    it('handles three-node cycle (A->B->C->A)', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'A', ParentID: 'C', Duration: 100 }),
        createSpan({ SpanID: 'B', ParentID: 'A', Duration: 200 }),
        createSpan({ SpanID: 'C', ParentID: 'B', Duration: 150 }),
        createSpan({ SpanID: 'root', ParentID: '', Duration: 75 }),
      ];

      // Should not throw, should handle gracefully
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBeGreaterThan(0);
    });

    it('handles cycle in subtree (root->child1, child1->child2->child1)', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: 100 }),
        createSpan({ SpanID: 'child1', ParentID: 'root', Duration: 200 }),
        createSpan({ SpanID: 'child2', ParentID: 'child1', Duration: 150 }),
        // Create cycle: child1 -> child2 -> child1
        createSpan({ SpanID: 'child3', ParentID: 'child2', Duration: 50 }),
      ];

      // Modify to create actual cycle
      spans[1].ParentID = 'child2'; // child1 now points to child2
      spans[2].ParentID = 'child1'; // child2 points to child1 (cycle)

      // Should not throw, should handle gracefully
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.has('root')).toBe(true);
    });

    it('handles circular references in SpanUI format', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'A', parent_span_id: 'B', duration_us: 100 }),
        createSpanUI({ span_id: 'B', parent_span_id: 'A', duration_us: 200 }),
      ];

      // Should not throw, should handle gracefully
      const result = computeCriticalPathUI(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBeGreaterThan(0);
    });
  });

  describe('invalid input handling', () => {
    it('handles null passed as spans', () => {
      // @ts-expect-error Testing invalid input
      const result = computeCriticalPath(null);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBe(0);
    });

    it('handles undefined passed as spans', () => {
      // @ts-expect-error Testing invalid input
      const result = computeCriticalPath(undefined);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBe(0);
    });

    it('handles array with null elements', () => {
      const spans = [
        createSpan({ SpanID: 'valid', ParentID: '', Duration: 100 }),
        null,
        createSpan({ SpanID: 'valid2', ParentID: '', Duration: 50 }),
        null,
      ];

      // @ts-expect-error Testing invalid input
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      // Should process valid spans only
      expect(result.size).toBeGreaterThan(0);
    });

    it('handles array with undefined elements', () => {
      const spans = [
        createSpan({ SpanID: 'valid', ParentID: '', Duration: 100 }),
        undefined,
        createSpan({ SpanID: 'valid2', ParentID: '', Duration: 50 }),
      ];

      // @ts-expect-error Testing invalid input
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBeGreaterThan(0);
    });

    it('handles array with objects missing span_id', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'valid', parent_span_id: '', duration_us: 100 }),
        // @ts-expect-error Testing invalid data
        { parent_span_id: '', duration_us: 50, service_name: 'test' }, // Missing span_id
        createSpanUI({ span_id: 'valid2', parent_span_id: '', duration_us: 75 }),
      ];

      const result = computeCriticalPathUI(spans);

      expect(result).toBeInstanceOf(Set);
      // Should process only valid spans
      expect(result.size).toBeGreaterThan(0);
    });

    it('handles objects with empty string span_id', () => {
      const spans: Span[] = [
        createSpan({ SpanID: '', ParentID: '', Duration: 100 }),
        createSpan({ SpanID: 'valid', ParentID: '', Duration: 50 }),
      ];

      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      // Empty string span_id should be filtered out
      expect(result.has('valid')).toBe(true);
    });
  });

  describe('invalid duration values', () => {
    it('handles NaN durations', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: NaN }),
        createSpan({ SpanID: 'child1', ParentID: 'root', Duration: 100 }),
        createSpan({ SpanID: 'child2', ParentID: 'root', Duration: NaN }),
      ];

      // Should not throw
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      // Should handle NaN gracefully (sanitized to 0)
      expect(result.size).toBeGreaterThan(0);
    });

    it('handles negative durations', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: -100 }),
        createSpan({ SpanID: 'child1', ParentID: 'root', Duration: 200 }),
        createSpan({ SpanID: 'child2', ParentID: 'root', Duration: -50 }),
      ];

      // Should not throw
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBeGreaterThan(0);
    });

    it('handles Infinity durations', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: Infinity }),
        createSpan({ SpanID: 'child1', ParentID: 'root', Duration: 100 }),
        createSpan({ SpanID: 'child2', ParentID: 'root', Duration: 200 }),
      ];

      // Should not throw
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBeGreaterThan(0);
    });

    it('handles negative Infinity durations', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: -Infinity }),
        createSpan({ SpanID: 'child', ParentID: 'root', Duration: 100 }),
      ];

      // Should not throw
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBeGreaterThan(0);
    });

    it('handles mixed valid and invalid durations', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: 100 }),
        createSpan({ SpanID: 'nan-child', ParentID: 'root', Duration: NaN }),
        createSpan({ SpanID: 'neg-child', ParentID: 'root', Duration: -50 }),
        createSpan({ SpanID: 'inf-child', ParentID: 'root', Duration: Infinity }),
        createSpan({ SpanID: 'valid-child', ParentID: 'root', Duration: 300 }),
      ];

      // Should not throw
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.has('root')).toBe(true);
      // Should select the valid child with longest duration
      expect(result.has('valid-child')).toBe(true);
    });

    it('handles NaN durations in SpanUI format', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'root', parent_span_id: '', duration_us: NaN }),
        createSpanUI({ span_id: 'child', parent_span_id: 'root', duration_us: 100 }),
      ];

      // Should not throw
      const result = computeCriticalPathUI(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBeGreaterThan(0);
    });
  });

  describe('deeply nested trees', () => {
    it('handles tree with 150 levels of nesting (stops at MAX_SPAN_DEPTH=100)', () => {
      const spans: Span[] = [];
      const depth = 150;

      // Create a deeply nested chain
      for (let i = 0; i < depth; i++) {
        spans.push(
          createSpan({
            SpanID: `level-${i}`,
            ParentID: i === 0 ? '' : `level-${i - 1}`,
            Duration: 10,
          })
        );
      }

      // Should not throw or hang
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      // Should stop at MAX_SPAN_DEPTH and return partial path
      // Depth 0-100 are processed (101 total), depth 101+ is skipped
      expect(result.size).toBeLessThanOrEqual(101);
      expect(result.size).toBeGreaterThan(0);
    });

    it('handles deeply nested tree in SpanUI format', () => {
      const spans: SpanUI[] = [];
      const depth = 150;

      for (let i = 0; i < depth; i++) {
        spans.push(
          createSpanUI({
            span_id: `level-${i}`,
            parent_span_id: i === 0 ? '' : `level-${i - 1}`,
            duration_us: 10,
          })
        );
      }

      // Should not throw or hang
      const result = computeCriticalPathUI(spans);

      expect(result).toBeInstanceOf(Set);
      // Depth 0-100 are processed (101 total), depth 101+ is skipped
      expect(result.size).toBeLessThanOrEqual(101);
      expect(result.size).toBeGreaterThan(0);
    });

    it('handles deeply nested tree with branching at each level', () => {
      const spans: Span[] = [];
      const depth = 120;

      // Create main chain
      for (let i = 0; i < depth; i++) {
        spans.push(
          createSpan({
            SpanID: `main-${i}`,
            ParentID: i === 0 ? '' : `main-${i - 1}`,
            Duration: 100,
          })
        );

        // Add a shorter sibling at each level
        if (i > 0) {
          spans.push(
            createSpan({
              SpanID: `side-${i}`,
              ParentID: `main-${i - 1}`,
              Duration: 10,
            })
          );
        }
      }

      // Should not throw
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBeGreaterThan(0);
    });
  });

  describe('mixed invalid data', () => {
    it('handles mixture of valid spans, NaN durations, and missing span_ids', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: 100 }),
        createSpan({ SpanID: 'valid-child', ParentID: 'root', Duration: 200 }),
        createSpan({ SpanID: 'nan-child', ParentID: 'root', Duration: NaN }),
        // @ts-expect-error Testing invalid data
        { ParentID: 'root', Duration: 150, TraceID: 'test' }, // Missing SpanID
        createSpan({ SpanID: '', ParentID: 'root', Duration: 175 }), // Empty SpanID
        createSpan({ SpanID: 'inf-child', ParentID: 'root', Duration: Infinity }),
      ];

      // Should not throw
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.has('root')).toBe(true);
      // Should select valid child with longest duration
      expect(result.has('valid-child')).toBe(true);
    });

    it('handles all invalid spans gracefully', () => {
      const spans = [
        { ParentID: '', Duration: 100 }, // Missing SpanID
        { SpanID: '', ParentID: '', Duration: 150 }, // Empty SpanID
        null,
        undefined,
      ];

      // @ts-expect-error Testing invalid input
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBe(0);
    });

    it('handles spans with non-string IDs', () => {
      const spans = [
        { SpanID: 123, ParentID: '', Duration: 100 },
        { SpanID: null, ParentID: '', Duration: 150 },
        createSpan({ SpanID: 'valid', ParentID: '', Duration: 75 }),
      ];

      // @ts-expect-error Testing invalid input
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      // Should only process valid span
      expect(result.has('valid')).toBe(true);
    });

    it('handles SpanUI with mixed invalid data', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'root', parent_span_id: '', duration_us: 100 }),
        createSpanUI({ span_id: 'valid', parent_span_id: 'root', duration_us: 200 }),
        createSpanUI({ span_id: 'nan', parent_span_id: 'root', duration_us: NaN }),
        // @ts-expect-error Testing invalid data
        { parent_span_id: 'root', duration_us: 150 }, // Missing span_id
        createSpanUI({ span_id: 'neg', parent_span_id: 'root', duration_us: -100 }),
      ];

      // Should not throw
      const result = computeCriticalPathUI(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.has('root')).toBe(true);
      expect(result.has('valid')).toBe(true);
    });
  });

  describe('extreme edge cases', () => {
    it('handles very large number of spans', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'root', ParentID: '', Duration: 100 }),
      ];

      // Create 1000 sibling spans
      for (let i = 0; i < 1000; i++) {
        spans.push(
          createSpan({
            SpanID: `child-${i}`,
            ParentID: 'root',
            Duration: i === 500 ? 1000 : 10, // child-500 is longest
          })
        );
      }

      // Should not throw or timeout
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBe(2);
      expect(result.has('root')).toBe(true);
      expect(result.has('child-500')).toBe(true);
    });

    it('handles combination of circular ref and invalid durations', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'A', ParentID: 'B', Duration: NaN }),
        createSpan({ SpanID: 'B', ParentID: 'A', Duration: Infinity }),
        createSpan({ SpanID: 'C', ParentID: '', Duration: -100 }),
      ];

      // Should not throw
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      // Should return some result even with malformed data
      expect(result.size).toBeGreaterThanOrEqual(0);
    });

    it('handles spans where all have same parent forming a cycle', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'A', ParentID: 'A', Duration: 100 }),
        createSpan({ SpanID: 'B', ParentID: 'B', Duration: 200 }),
        createSpan({ SpanID: 'C', ParentID: 'C', Duration: 150 }),
      ];

      // Should not throw
      const result = computeCriticalPath(spans);

      expect(result).toBeInstanceOf(Set);
      expect(result.size).toBeGreaterThan(0);
    });
  });
});
