import { describe, it, expect } from 'vitest';
import {
  formatDuration,
  computeSpanDepth,
  computeSpanTiming,
  getTraceDuration,
  computeSpanUIDepth,
  computeSpanUITiming,
  getTraceDurationFromUI,
} from './traceUtils';
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

describe('formatDuration', () => {
  describe('microseconds (< 1ms)', () => {
    it('formats sub-millisecond values correctly', () => {
      expect(formatDuration(500)).toBe('0.50ms');
    });

    it('formats very small values', () => {
      expect(formatDuration(1)).toBe('0.00ms');
    });

    it('formats 100 microseconds', () => {
      expect(formatDuration(100)).toBe('0.10ms');
    });
  });

  describe('milliseconds', () => {
    it('formats 1ms correctly', () => {
      expect(formatDuration(1000)).toBe('1.00ms');
    });

    it('formats 1.23ms correctly', () => {
      expect(formatDuration(1230)).toBe('1.23ms');
    });

    it('formats values just under 1 second', () => {
      expect(formatDuration(999000)).toBe('999.00ms');
    });

    it('formats fractional milliseconds', () => {
      expect(formatDuration(1500)).toBe('1.50ms');
    });
  });

  describe('seconds', () => {
    it('formats 1 second correctly', () => {
      expect(formatDuration(1000000)).toBe('1.00s');
    });

    it('formats 1.23s correctly', () => {
      expect(formatDuration(1230000)).toBe('1.23s');
    });

    it('formats values just under 1 minute', () => {
      expect(formatDuration(59000000)).toBe('59.00s');
    });

    it('formats 30 seconds', () => {
      expect(formatDuration(30000000)).toBe('30.00s');
    });
  });

  describe('minutes', () => {
    it('formats 1 minute correctly', () => {
      expect(formatDuration(60000000)).toBe('1.00m');
    });

    it('formats 1.5 minutes correctly', () => {
      expect(formatDuration(90000000)).toBe('1.50m');
    });

    it('formats 5 minutes', () => {
      expect(formatDuration(300000000)).toBe('5.00m');
    });
  });

  describe('edge cases', () => {
    it('handles zero', () => {
      expect(formatDuration(0)).toBe('0.00ms');
    });

    it('handles negative values', () => {
      expect(formatDuration(-100)).toBe('0.00ms');
    });

    it('handles very large values (hours)', () => {
      // 2 hours = 120 minutes
      expect(formatDuration(7200000000)).toBe('120.00m');
    });

    it('handles very small positive values', () => {
      expect(formatDuration(0.001)).toBe('0.00ms');
    });
  });
});

describe('computeSpanDepth', () => {
  describe('root spans', () => {
    it('returns depth 0 for root span with empty ParentID', () => {
      const rootSpan = createSpan({ SpanID: 'root', ParentID: '' });
      const spans = [rootSpan];

      expect(computeSpanDepth(rootSpan, spans)).toBe(0);
    });

    it('returns depth 0 for root span in multi-span trace', () => {
      const rootSpan = createSpan({ SpanID: 'root', ParentID: '' });
      const childSpan = createSpan({ SpanID: 'child', ParentID: 'root' });
      const spans = [rootSpan, childSpan];

      expect(computeSpanDepth(rootSpan, spans)).toBe(0);
    });
  });

  describe('child spans', () => {
    it('returns depth 1 for direct child of root', () => {
      const rootSpan = createSpan({ SpanID: 'root', ParentID: '' });
      const childSpan = createSpan({ SpanID: 'child', ParentID: 'root' });
      const spans = [rootSpan, childSpan];

      expect(computeSpanDepth(childSpan, spans)).toBe(1);
    });

    it('returns depth 2 for grandchild of root', () => {
      const rootSpan = createSpan({ SpanID: 'root', ParentID: '' });
      const childSpan = createSpan({ SpanID: 'child', ParentID: 'root' });
      const grandchildSpan = createSpan({ SpanID: 'grandchild', ParentID: 'child' });
      const spans = [rootSpan, childSpan, grandchildSpan];

      expect(computeSpanDepth(grandchildSpan, spans)).toBe(2);
    });
  });

  describe('deeply nested spans', () => {
    it('returns correct depth for deeply nested spans', () => {
      const spans: Span[] = [];
      const depth = 10;

      for (let i = 0; i < depth; i++) {
        spans.push(
          createSpan({
            SpanID: `level-${i}`,
            ParentID: i === 0 ? '' : `level-${i - 1}`,
          })
        );
      }

      // Check each level has correct depth
      for (let i = 0; i < depth; i++) {
        expect(computeSpanDepth(spans[i], spans)).toBe(i);
      }
    });
  });

  describe('orphan spans', () => {
    it('returns depth 0 for orphan span (parent not found)', () => {
      const orphanSpan = createSpan({ SpanID: 'orphan', ParentID: 'non-existent' });
      const spans = [orphanSpan];

      expect(computeSpanDepth(orphanSpan, spans)).toBe(0);
    });

    it('returns depth 0 for orphan with other spans present', () => {
      const rootSpan = createSpan({ SpanID: 'root', ParentID: '' });
      const orphanSpan = createSpan({ SpanID: 'orphan', ParentID: 'missing-parent' });
      const spans = [rootSpan, orphanSpan];

      expect(computeSpanDepth(orphanSpan, spans)).toBe(0);
    });
  });

  describe('sibling spans', () => {
    it('returns same depth for sibling spans', () => {
      const rootSpan = createSpan({ SpanID: 'root', ParentID: '' });
      const child1 = createSpan({ SpanID: 'child-1', ParentID: 'root' });
      const child2 = createSpan({ SpanID: 'child-2', ParentID: 'root' });
      const child3 = createSpan({ SpanID: 'child-3', ParentID: 'root' });
      const spans = [rootSpan, child1, child2, child3];

      expect(computeSpanDepth(child1, spans)).toBe(1);
      expect(computeSpanDepth(child2, spans)).toBe(1);
      expect(computeSpanDepth(child3, spans)).toBe(1);
    });
  });
});

describe('computeSpanTiming', () => {
  describe('empty trace', () => {
    it('returns empty map for empty spans array', () => {
      const result = computeSpanTiming([]);
      expect(result.size).toBe(0);
    });
  });

  describe('single span trace', () => {
    it('returns correct timing for single span', () => {
      const span = createSpan({
        SpanID: 'only-span',
        StartTime: 1000000,
        Duration: 10000,
      });

      const result = computeSpanTiming([span]);

      expect(result.size).toBe(1);
      const timing = result.get('only-span');
      expect(timing).toBeDefined();
      expect(timing!.offsetPercent).toBe(0);
      expect(timing!.widthPercent).toBe(100);
    });
  });

  describe('multiple spans with correct offsets and widths', () => {
    it('calculates offset and width percentages correctly', () => {
      // Trace: |----root----|
      //        |--child--|
      // Total duration: 1000
      const spans: Span[] = [
        createSpan({
          SpanID: 'root',
          ParentID: '',
          StartTime: 0,
          Duration: 1000,
        }),
        createSpan({
          SpanID: 'child',
          ParentID: 'root',
          StartTime: 100,
          Duration: 500,
        }),
      ];

      const result = computeSpanTiming(spans);

      // Root: starts at 0, duration 1000 out of 1000 = 100%
      const rootTiming = result.get('root');
      expect(rootTiming!.offsetPercent).toBe(0);
      expect(rootTiming!.widthPercent).toBe(100);

      // Child: starts at 100, offset = 100/1000 = 10%, width = 500/1000 = 50%
      const childTiming = result.get('child');
      expect(childTiming!.offsetPercent).toBe(10);
      expect(childTiming!.widthPercent).toBe(50);
    });

    it('handles spans that start after root', () => {
      const spans: Span[] = [
        createSpan({
          SpanID: 'root',
          StartTime: 1000,
          Duration: 500,
        }),
        createSpan({
          SpanID: 'late-child',
          ParentID: 'root',
          StartTime: 1250, // Starts at midpoint of root
          Duration: 250,
        }),
      ];

      const result = computeSpanTiming(spans);

      // Total trace: 1000 to 1500 = 500
      // Root: offset 0, width 100%
      const rootTiming = result.get('root');
      expect(rootTiming!.offsetPercent).toBe(0);
      expect(rootTiming!.widthPercent).toBe(100);

      // Late child: offset = 250/500 = 50%, width = 250/500 = 50%
      const lateTiming = result.get('late-child');
      expect(lateTiming!.offsetPercent).toBe(50);
      expect(lateTiming!.widthPercent).toBe(50);
    });
  });

  describe('overlapping spans', () => {
    it('handles overlapping parallel spans', () => {
      // Two parallel spans that overlap
      const spans: Span[] = [
        createSpan({
          SpanID: 'span-a',
          StartTime: 0,
          Duration: 600,
        }),
        createSpan({
          SpanID: 'span-b',
          StartTime: 400,
          Duration: 600, // Ends at 1000
        }),
      ];

      const result = computeSpanTiming(spans);

      // Total trace: 0 to 1000 = 1000
      // Span A: offset 0, width 60%
      const timingA = result.get('span-a');
      expect(timingA!.offsetPercent).toBe(0);
      expect(timingA!.widthPercent).toBe(60);

      // Span B: offset 40%, width 60%
      const timingB = result.get('span-b');
      expect(timingB!.offsetPercent).toBe(40);
      expect(timingB!.widthPercent).toBe(60);
    });

    it('handles child that extends beyond parent', () => {
      const spans: Span[] = [
        createSpan({
          SpanID: 'parent',
          StartTime: 100,
          Duration: 200, // Ends at 300
        }),
        createSpan({
          SpanID: 'long-child',
          ParentID: 'parent',
          StartTime: 150,
          Duration: 350, // Ends at 500
        }),
      ];

      const result = computeSpanTiming(spans);

      // Total trace: 100 to 500 = 400
      // Parent: offset 0, width 200/400 = 50%
      const parentTiming = result.get('parent');
      expect(parentTiming!.offsetPercent).toBe(0);
      expect(parentTiming!.widthPercent).toBe(50);

      // Child: offset = 50/400 = 12.5%, width = 350/400 = 87.5%
      const childTiming = result.get('long-child');
      expect(childTiming!.offsetPercent).toBe(12.5);
      expect(childTiming!.widthPercent).toBe(87.5);
    });
  });

  describe('zero-duration spans', () => {
    it('handles single zero-duration span', () => {
      const span = createSpan({
        SpanID: 'instant',
        StartTime: 1000,
        Duration: 0,
      });

      const result = computeSpanTiming([span]);

      const timing = result.get('instant');
      expect(timing).toBeDefined();
      expect(timing!.offsetPercent).toBe(0);
      // Single span gets 100% width when total duration is 0
      expect(timing!.widthPercent).toBe(100);
    });

    it('handles multiple zero-duration spans at same time', () => {
      const spans: Span[] = [
        createSpan({
          SpanID: 'instant-1',
          StartTime: 1000,
          Duration: 0,
        }),
        createSpan({
          SpanID: 'instant-2',
          StartTime: 1000,
          Duration: 0,
        }),
      ];

      const result = computeSpanTiming(spans);

      // When total duration is 0, each span gets 100/N percent width
      const timing1 = result.get('instant-1');
      const timing2 = result.get('instant-2');
      expect(timing1!.offsetPercent).toBe(0);
      expect(timing1!.widthPercent).toBe(50);
      expect(timing2!.offsetPercent).toBe(0);
      expect(timing2!.widthPercent).toBe(50);
    });

    it('applies minimum width for very short spans', () => {
      const spans: Span[] = [
        createSpan({
          SpanID: 'long',
          StartTime: 0,
          Duration: 100000, // 100ms
        }),
        createSpan({
          SpanID: 'short',
          ParentID: 'long',
          StartTime: 50000,
          Duration: 1, // 1 microsecond
        }),
      ];

      const result = computeSpanTiming(spans);

      // Short span's calculated width would be 0.001%
      // But minimum width should be 0.5%
      const shortTiming = result.get('short');
      expect(shortTiming!.widthPercent).toBeGreaterThanOrEqual(0.5);
    });
  });

  describe('edge cases', () => {
    it('handles spans with same start and end time', () => {
      const spans: Span[] = [
        createSpan({
          SpanID: 'span-1',
          StartTime: 500,
          Duration: 100,
        }),
        createSpan({
          SpanID: 'span-2',
          StartTime: 500,
          Duration: 100,
        }),
      ];

      const result = computeSpanTiming(spans);

      // Both spans have same timing
      const timing1 = result.get('span-1');
      const timing2 = result.get('span-2');
      expect(timing1!.offsetPercent).toBe(0);
      expect(timing1!.widthPercent).toBe(100);
      expect(timing2!.offsetPercent).toBe(0);
      expect(timing2!.widthPercent).toBe(100);
    });
  });
});

describe('getTraceDuration', () => {
  describe('empty trace', () => {
    it('returns 0 for empty spans array', () => {
      expect(getTraceDuration([])).toBe(0);
    });
  });

  describe('single span', () => {
    it('returns span duration for single span', () => {
      const span = createSpan({
        SpanID: 'only-span',
        StartTime: 1000000,
        Duration: 50000,
      });

      expect(getTraceDuration([span])).toBe(50000);
    });

    it('handles zero-duration single span', () => {
      const span = createSpan({
        SpanID: 'instant',
        StartTime: 1000000,
        Duration: 0,
      });

      expect(getTraceDuration([span])).toBe(0);
    });
  });

  describe('sequential spans', () => {
    it('returns total duration from earliest start to latest end', () => {
      // Trace: |--A--|--B--|
      // A: 0-100, B: 100-250
      // Total: 250
      const spans: Span[] = [
        createSpan({
          SpanID: 'span-a',
          StartTime: 0,
          Duration: 100,
        }),
        createSpan({
          SpanID: 'span-b',
          StartTime: 100,
          Duration: 150,
        }),
      ];

      expect(getTraceDuration(spans)).toBe(250);
    });
  });

  describe('parallel spans', () => {
    it('returns duration based on earliest start and latest end', () => {
      // Parallel spans:
      // A: |-----------|  (0 to 500)
      // B:    |---|       (100 to 300)
      // Total: 500
      const spans: Span[] = [
        createSpan({
          SpanID: 'span-a',
          StartTime: 0,
          Duration: 500,
        }),
        createSpan({
          SpanID: 'span-b',
          StartTime: 100,
          Duration: 200,
        }),
      ];

      expect(getTraceDuration(spans)).toBe(500);
    });

    it('handles parallel spans where child extends beyond parent', () => {
      // Parent: |-----|     (0 to 100)
      // Child:    |-------| (50 to 200)
      // Total: 200
      const spans: Span[] = [
        createSpan({
          SpanID: 'parent',
          StartTime: 0,
          Duration: 100,
        }),
        createSpan({
          SpanID: 'child',
          ParentID: 'parent',
          StartTime: 50,
          Duration: 150,
        }),
      ];

      expect(getTraceDuration(spans)).toBe(200);
    });

    it('handles multiple parallel spans with different timings', () => {
      // A: |--------|       (0 to 400)
      // B:   |--|           (100 to 200)
      // C:       |--------|  (300 to 600)
      // Total: 600
      const spans: Span[] = [
        createSpan({
          SpanID: 'span-a',
          StartTime: 0,
          Duration: 400,
        }),
        createSpan({
          SpanID: 'span-b',
          StartTime: 100,
          Duration: 100,
        }),
        createSpan({
          SpanID: 'span-c',
          StartTime: 300,
          Duration: 300,
        }),
      ];

      expect(getTraceDuration(spans)).toBe(600);
    });
  });

  describe('edge cases', () => {
    it('handles spans with gaps between them', () => {
      // A: |--|             (0 to 100)
      // B:        |--|      (200 to 300)
      // Total: 300 (includes gap)
      const spans: Span[] = [
        createSpan({
          SpanID: 'span-a',
          StartTime: 0,
          Duration: 100,
        }),
        createSpan({
          SpanID: 'span-b',
          StartTime: 200,
          Duration: 100,
        }),
      ];

      expect(getTraceDuration(spans)).toBe(300);
    });

    it('handles all spans starting at same time', () => {
      const spans: Span[] = [
        createSpan({
          SpanID: 'span-a',
          StartTime: 1000,
          Duration: 100,
        }),
        createSpan({
          SpanID: 'span-b',
          StartTime: 1000,
          Duration: 200,
        }),
        createSpan({
          SpanID: 'span-c',
          StartTime: 1000,
          Duration: 150,
        }),
      ];

      // All start at 1000, longest ends at 1200
      expect(getTraceDuration(spans)).toBe(200);
    });

    it('handles very large timestamps', () => {
      const baseTime = 1700000000000000; // Large unix microseconds
      const spans: Span[] = [
        createSpan({
          SpanID: 'span-a',
          StartTime: baseTime,
          Duration: 1000000,
        }),
      ];

      expect(getTraceDuration(spans)).toBe(1000000);
    });
  });
});

describe('Robustness and Edge Cases', () => {
  describe('computeSpanDepth with edge cases', () => {
    it('handles circular parent references (A->B->C->A)', () => {
      // Create a circular reference
      const spanA = createSpan({ SpanID: 'A', ParentID: 'C' });
      const spanB = createSpan({ SpanID: 'B', ParentID: 'A' });
      const spanC = createSpan({ SpanID: 'C', ParentID: 'B' });
      const spans = [spanA, spanB, spanC];

      // Should not crash and should return bounded depth
      expect(() => computeSpanDepth(spanA, spans)).not.toThrow();
      expect(() => computeSpanDepth(spanB, spans)).not.toThrow();
      expect(() => computeSpanDepth(spanC, spans)).not.toThrow();

      const depthA = computeSpanDepth(spanA, spans);
      const depthB = computeSpanDepth(spanB, spans);
      const depthC = computeSpanDepth(spanC, spans);

      // Depths should be bounded (not infinite)
      expect(depthA).toBeGreaterThanOrEqual(0);
      expect(depthA).toBeLessThan(1000);
      expect(depthB).toBeGreaterThanOrEqual(0);
      expect(depthB).toBeLessThan(1000);
      expect(depthC).toBeGreaterThanOrEqual(0);
      expect(depthC).toBeLessThan(1000);
    });

    it('handles missing parent in spanMap', () => {
      const span = createSpan({ SpanID: 'child', ParentID: 'missing-parent' });
      const spans = [span];

      // Should not crash, should return 0 (treated as orphan)
      expect(() => computeSpanDepth(span, spans)).not.toThrow();
      expect(computeSpanDepth(span, spans)).toBe(0);
    });

    it('handles invalid span with null span_id', () => {
      const invalidSpan = createSpan({ SpanID: null as any });
      const spans = [invalidSpan];

      // Should not crash
      expect(() => computeSpanDepth(invalidSpan, spans)).not.toThrow();
      expect(computeSpanDepth(invalidSpan, spans)).toBe(0);
    });

    it('handles empty spans array', () => {
      const span = createSpan({ SpanID: 'test', ParentID: 'parent' });

      // Should not crash
      expect(() => computeSpanDepth(span, [])).not.toThrow();
      expect(computeSpanDepth(span, [])).toBe(0);
    });

    it('handles span with undefined SpanID', () => {
      const invalidSpan = createSpan({ SpanID: undefined as any });
      const spans = [invalidSpan];

      // Should not crash
      expect(() => computeSpanDepth(invalidSpan, spans)).not.toThrow();
      expect(computeSpanDepth(invalidSpan, spans)).toBe(0);
    });
  });

  describe('computeSpanUIDepth with edge cases', () => {
    it('handles circular parent references (A->B->C->A)', () => {
      const spanA = createSpanUI({ span_id: 'A', parent_span_id: 'C' });
      const spanB = createSpanUI({ span_id: 'B', parent_span_id: 'A' });
      const spanC = createSpanUI({ span_id: 'C', parent_span_id: 'B' });
      const spans = [spanA, spanB, spanC];

      // Should not crash and should return bounded depth
      expect(() => computeSpanUIDepth(spanA, spans)).not.toThrow();
      expect(() => computeSpanUIDepth(spanB, spans)).not.toThrow();
      expect(() => computeSpanUIDepth(spanC, spans)).not.toThrow();

      const depthA = computeSpanUIDepth(spanA, spans);
      const depthB = computeSpanUIDepth(spanB, spans);
      const depthC = computeSpanUIDepth(spanC, spans);

      expect(depthA).toBeGreaterThanOrEqual(0);
      expect(depthA).toBeLessThan(1000);
      expect(depthB).toBeGreaterThanOrEqual(0);
      expect(depthB).toBeLessThan(1000);
      expect(depthC).toBeGreaterThanOrEqual(0);
      expect(depthC).toBeLessThan(1000);
    });

    it('handles missing parent in spanMap', () => {
      const span = createSpanUI({ span_id: 'child', parent_span_id: 'missing' });
      const spans = [span];

      expect(() => computeSpanUIDepth(span, spans)).not.toThrow();
      expect(computeSpanUIDepth(span, spans)).toBe(0);
    });

    it('handles invalid span with null span_id', () => {
      const invalidSpan = createSpanUI({ span_id: null as any });
      const spans = [invalidSpan];

      expect(() => computeSpanUIDepth(invalidSpan, spans)).not.toThrow();
      expect(computeSpanUIDepth(invalidSpan, spans)).toBe(0);
    });

    it('handles empty spans array', () => {
      const span = createSpanUI({ span_id: 'test', parent_span_id: 'parent' });

      expect(() => computeSpanUIDepth(span, [])).not.toThrow();
      expect(computeSpanUIDepth(span, [])).toBe(0);
    });
  });

  describe('formatDuration with invalid inputs', () => {
    it('handles NaN', () => {
      expect(() => formatDuration(NaN)).not.toThrow();
      expect(formatDuration(NaN)).toBe('0.00ms');
    });

    it('handles undefined', () => {
      expect(() => formatDuration(undefined)).not.toThrow();
      expect(formatDuration(undefined)).toBe('0.00ms');
    });

    it('handles null', () => {
      expect(() => formatDuration(null)).not.toThrow();
      expect(formatDuration(null)).toBe('0.00ms');
    });

    it('handles Infinity', () => {
      expect(() => formatDuration(Infinity)).not.toThrow();
      // Infinity should be sanitized to 0
      const result = formatDuration(Infinity);
      expect(result).toMatch(/^(\d+\.\d+)(ms|s|m)$/);
    });

    it('handles negative Infinity', () => {
      expect(() => formatDuration(-Infinity)).not.toThrow();
      expect(formatDuration(-Infinity)).toBe('0.00ms');
    });

    it('handles string "123"', () => {
      expect(() => formatDuration('123' as any)).not.toThrow();
      // String "123" gets converted to number 123 via Number() conversion
      // 123 microseconds = 0.123ms
      expect(formatDuration('123' as any)).toBe('0.12ms');
    });

    it('handles empty string', () => {
      expect(() => formatDuration('' as any)).not.toThrow();
      expect(formatDuration('' as any)).toBe('0.00ms');
    });

    it('handles object', () => {
      expect(() => formatDuration({} as any)).not.toThrow();
      expect(formatDuration({} as any)).toBe('0.00ms');
    });

    it('handles array', () => {
      expect(() => formatDuration([123] as any)).not.toThrow();
      // Single-element array [123] gets converted to number 123 via Number() conversion
      // 123 microseconds = 0.123ms
      expect(formatDuration([123] as any)).toBe('0.12ms');
    });

    it('handles boolean', () => {
      expect(() => formatDuration(true as any)).not.toThrow();
      expect(formatDuration(true as any)).toBe('0.00ms');
    });
  });

  describe('computeSpanTiming with invalid inputs', () => {
    it('handles all spans with NaN start_time', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'span1', StartTime: NaN, Duration: 100 }),
        createSpan({ SpanID: 'span2', StartTime: NaN, Duration: 200 }),
      ];

      expect(() => computeSpanTiming(spans)).not.toThrow();
      const result = computeSpanTiming(spans);

      // Should distribute evenly when all timing is invalid
      expect(result.size).toBeGreaterThan(0);
      result.forEach(timing => {
        expect(timing.offsetPercent).toBeGreaterThanOrEqual(0);
        expect(timing.widthPercent).toBeGreaterThanOrEqual(0);
      });
    });

    it('handles all spans with NaN duration', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'span1', StartTime: 1000, Duration: NaN }),
        createSpan({ SpanID: 'span2', StartTime: 2000, Duration: NaN }),
      ];

      expect(() => computeSpanTiming(spans)).not.toThrow();
      const result = computeSpanTiming(spans);

      expect(result.size).toBeGreaterThan(0);
      result.forEach(timing => {
        expect(timing.offsetPercent).toBeGreaterThanOrEqual(0);
        expect(timing.widthPercent).toBeGreaterThanOrEqual(0);
      });
    });

    it('handles mixed valid and invalid spans', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'valid', StartTime: 1000, Duration: 100 }),
        createSpan({ SpanID: 'invalid1', StartTime: NaN, Duration: 200 }),
        createSpan({ SpanID: 'invalid2', StartTime: 2000, Duration: NaN }),
      ];

      expect(() => computeSpanTiming(spans)).not.toThrow();
      const result = computeSpanTiming(spans);

      // Should handle the valid span
      expect(result.has('valid')).toBe(true);
      const validTiming = result.get('valid');
      expect(validTiming?.offsetPercent).toBeGreaterThanOrEqual(0);
      expect(validTiming?.widthPercent).toBeGreaterThanOrEqual(0);
    });

    it('handles spans with negative duration', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'normal', StartTime: 1000, Duration: 100 }),
        createSpan({ SpanID: 'negative', StartTime: 2000, Duration: -50 }),
      ];

      expect(() => computeSpanTiming(spans)).not.toThrow();
      const result = computeSpanTiming(spans);

      // Negative durations should be sanitized
      expect(result.has('normal')).toBe(true);
      expect(result.has('negative')).toBe(true);
    });

    it('handles null passed instead of array', () => {
      expect(() => computeSpanTiming(null as any)).not.toThrow();
      const result = computeSpanTiming(null as any);
      expect(result.size).toBe(0);
    });

    it('handles undefined passed instead of array', () => {
      expect(() => computeSpanTiming(undefined as any)).not.toThrow();
      const result = computeSpanTiming(undefined as any);
      expect(result.size).toBe(0);
    });

    it('handles array with all invalid spans', () => {
      const spans: Span[] = [
        createSpan({ SpanID: null as any, StartTime: NaN, Duration: NaN }),
        createSpan({ SpanID: undefined as any, StartTime: NaN, Duration: NaN }),
      ];

      expect(() => computeSpanTiming(spans)).not.toThrow();
      const result = computeSpanTiming(spans);
      // Invalid spans should be filtered out
      expect(result.size).toBe(0);
    });
  });

  describe('computeSpanUITiming with invalid inputs', () => {
    it('handles all spans with NaN start_time', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'span1', start_time: NaN, duration_us: 100 }),
        createSpanUI({ span_id: 'span2', start_time: NaN, duration_us: 200 }),
      ];

      expect(() => computeSpanUITiming(spans)).not.toThrow();
      const result = computeSpanUITiming(spans);

      expect(result.size).toBeGreaterThan(0);
      result.forEach(timing => {
        expect(timing.offsetPercent).toBeGreaterThanOrEqual(0);
        expect(timing.widthPercent).toBeGreaterThanOrEqual(0);
      });
    });

    it('handles all spans with NaN duration', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'span1', start_time: 1000, duration_us: NaN }),
        createSpanUI({ span_id: 'span2', start_time: 2000, duration_us: NaN }),
      ];

      expect(() => computeSpanUITiming(spans)).not.toThrow();
      const result = computeSpanUITiming(spans);

      expect(result.size).toBeGreaterThan(0);
    });

    it('handles mixed valid and invalid spans', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'valid', start_time: 1000, duration_us: 100 }),
        createSpanUI({ span_id: 'invalid', start_time: NaN, duration_us: 200 }),
      ];

      expect(() => computeSpanUITiming(spans)).not.toThrow();
      const result = computeSpanUITiming(spans);

      expect(result.has('valid')).toBe(true);
    });

    it('handles spans with negative duration', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'normal', start_time: 1000, duration_us: 100 }),
        createSpanUI({ span_id: 'negative', start_time: 2000, duration_us: -50 }),
      ];

      expect(() => computeSpanUITiming(spans)).not.toThrow();
      const result = computeSpanUITiming(spans);

      expect(result.has('normal')).toBe(true);
      expect(result.has('negative')).toBe(true);
    });
  });

  describe('getTraceDuration with invalid inputs', () => {
    it('handles null passed instead of array', () => {
      expect(() => getTraceDuration(null as any)).not.toThrow();
      expect(getTraceDuration(null as any)).toBe(0);
    });

    it('handles undefined passed instead of array', () => {
      expect(() => getTraceDuration(undefined as any)).not.toThrow();
      expect(getTraceDuration(undefined as any)).toBe(0);
    });

    it('handles array with null elements', () => {
      const spans = [
        createSpan({ SpanID: 'valid', StartTime: 1000, Duration: 100 }),
        null as any,
        createSpan({ SpanID: 'valid2', StartTime: 2000, Duration: 200 }),
      ];

      expect(() => getTraceDuration(spans)).not.toThrow();
      const duration = getTraceDuration(spans);
      // Should handle valid spans and skip null
      expect(duration).toBeGreaterThanOrEqual(0);
    });

    it('handles all invalid spans', () => {
      const spans: Span[] = [
        createSpan({ SpanID: null as any, StartTime: NaN, Duration: NaN }),
        createSpan({ SpanID: undefined as any, StartTime: NaN, Duration: NaN }),
      ];

      expect(() => getTraceDuration(spans)).not.toThrow();
      expect(getTraceDuration(spans)).toBe(0);
    });

    it('handles mixed valid and invalid spans', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'valid', StartTime: 1000, Duration: 100 }),
        createSpan({ SpanID: 'invalid', StartTime: NaN, Duration: NaN }),
      ];

      expect(() => getTraceDuration(spans)).not.toThrow();
      const duration = getTraceDuration(spans);
      expect(duration).toBeGreaterThanOrEqual(0);
    });

    it('handles spans with negative duration', () => {
      const spans: Span[] = [
        createSpan({ SpanID: 'normal', StartTime: 1000, Duration: 100 }),
        createSpan({ SpanID: 'negative', StartTime: 1000, Duration: -50 }),
      ];

      expect(() => getTraceDuration(spans)).not.toThrow();
      const duration = getTraceDuration(spans);
      // Negative durations should be sanitized
      expect(duration).toBeGreaterThanOrEqual(0);
    });

    it('handles empty string as input', () => {
      expect(() => getTraceDuration('' as any)).not.toThrow();
      expect(getTraceDuration('' as any)).toBe(0);
    });

    it('handles object as input', () => {
      expect(() => getTraceDuration({} as any)).not.toThrow();
      expect(getTraceDuration({} as any)).toBe(0);
    });
  });

  describe('getTraceDurationFromUI with invalid inputs', () => {
    it('handles null passed instead of array', () => {
      expect(() => getTraceDurationFromUI(null as any)).not.toThrow();
      expect(getTraceDurationFromUI(null as any)).toBe(0);
    });

    it('handles undefined passed instead of array', () => {
      expect(() => getTraceDurationFromUI(undefined as any)).not.toThrow();
      expect(getTraceDurationFromUI(undefined as any)).toBe(0);
    });

    it('handles array with null elements', () => {
      const spans = [
        createSpanUI({ span_id: 'valid', start_time: 1000, duration_us: 100 }),
        null as any,
        createSpanUI({ span_id: 'valid2', start_time: 2000, duration_us: 200 }),
      ];

      expect(() => getTraceDurationFromUI(spans)).not.toThrow();
      const duration = getTraceDurationFromUI(spans);
      expect(duration).toBeGreaterThanOrEqual(0);
    });

    it('handles all invalid spans', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: null as any, start_time: NaN, duration_us: NaN }),
        createSpanUI({ span_id: undefined as any, start_time: NaN, duration_us: NaN }),
      ];

      expect(() => getTraceDurationFromUI(spans)).not.toThrow();
      expect(getTraceDurationFromUI(spans)).toBe(0);
    });

    it('handles mixed valid and invalid spans', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'valid', start_time: 1000, duration_us: 100 }),
        createSpanUI({ span_id: 'invalid', start_time: NaN, duration_us: NaN }),
      ];

      expect(() => getTraceDurationFromUI(spans)).not.toThrow();
      const duration = getTraceDurationFromUI(spans);
      expect(duration).toBeGreaterThanOrEqual(0);
    });

    it('handles spans with negative duration', () => {
      const spans: SpanUI[] = [
        createSpanUI({ span_id: 'normal', start_time: 1000, duration_us: 100 }),
        createSpanUI({ span_id: 'negative', start_time: 1000, duration_us: -50 }),
      ];

      expect(() => getTraceDurationFromUI(spans)).not.toThrow();
      const duration = getTraceDurationFromUI(spans);
      expect(duration).toBeGreaterThanOrEqual(0);
    });
  });
});
