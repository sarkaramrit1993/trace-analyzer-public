import { render, screen, fireEvent, within } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { TraceDetailView } from './TraceDetailView';
import type { TraceDetail, SpanUI, ServiceGrouping } from '../../types';

// Mock Service Grouping
const mockServiceGrouping: ServiceGrouping = {
  service_identity: 'api-gateway',
  scope1: 'platform',
  scope2: 'frontend',
  scope3: 'web',
  env: 'prod',
  operation: 'handleRequest',
  feature_group: 'core',
  feature_name: 'routing',
  sub_service: 'main',
};

// Mock spans with parent-child relationships
const mockSpans: SpanUI[] = [
  {
    span_id: 'span-root-1',
    parent_span_id: '',
    service_name: 'api-gateway',
    operation_name: 'handleRequest',
    start_time: 1705315800000000,
    duration_us: 150000,
    status: 'OK',
    attributes: { 'http.method': 'GET', 'http.url': '/api/users' },
  },
  {
    span_id: 'span-child-1',
    parent_span_id: 'span-root-1',
    service_name: 'user-service',
    operation_name: 'getUser',
    start_time: 1705315800050000,
    duration_us: 80000,
    status: 'OK',
    attributes: { 'db.type': 'postgres', 'db.statement': 'SELECT * FROM users' },
  },
  {
    span_id: 'span-child-2',
    parent_span_id: 'span-root-1',
    service_name: 'cache-service',
    operation_name: 'getFromCache',
    start_time: 1705315800010000,
    duration_us: 5000,
    status: 'OK',
    attributes: { 'cache.hit': 'true' },
  },
  {
    span_id: 'span-grandchild-1',
    parent_span_id: 'span-child-1',
    service_name: 'database',
    operation_name: 'query',
    start_time: 1705315800060000,
    duration_us: 50000,
    status: 'OK',
    attributes: { 'db.rows': '1' },
  },
];

// Mock error span
const mockErrorSpan: SpanUI = {
  span_id: 'span-error-1',
  parent_span_id: 'span-root-1',
  service_name: 'payment-service',
  operation_name: 'processPayment',
  start_time: 1705315800100000,
  duration_us: 25000,
  status: 'ERROR',
  attributes: { 'error.message': 'Payment failed', 'error.code': '500' },
};

// Full mock trace
const mockTrace: TraceDetail = {
  trace_id: 'trace-abc123def456',
  service_id: 'api-gateway:handleRequest',
  service_grouping: mockServiceGrouping,
  fingerprint: 'fp-hash-12345678',
  total_duration_us: 150000,
  has_error: false,
  spans: mockSpans,
};

// Mock trace with errors
const mockTraceWithError: TraceDetail = {
  trace_id: 'trace-error-789',
  service_id: 'api-gateway:handleRequest',
  service_grouping: mockServiceGrouping,
  fingerprint: 'fp-hash-error-456',
  total_duration_us: 200000,
  has_error: true,
  spans: [...mockSpans, mockErrorSpan],
};

const spanTree = () => within(screen.getByText('Span Tree').parentElement!);
const metricCard = (label: string) => within(screen.getByText(label).parentElement!);

describe('TraceDetailView', () => {
  const mockOnClose = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('Trace ID Display', () => {
    it('renders the trace ID', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      expect(screen.getByText('trace-abc123def456')).toBeInTheDocument();
    });

    it('renders "Trace Detail" heading', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      expect(screen.getByText('Trace Detail')).toBeInTheDocument();
    });

    it('trace ID is displayed in monospace font', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      const traceIdElement = screen.getByText('trace-abc123def456');
      expect(traceIdElement).toHaveClass('font-mono');
    });

    it('handles long trace IDs', () => {
      const longIdTrace = {
        ...mockTrace,
        trace_id: 'very-long-trace-id-1234567890abcdef1234567890abcdef',
      };
      render(<TraceDetailView trace={longIdTrace} onClose={mockOnClose} />);
      expect(screen.getByText('very-long-trace-id-1234567890abcdef1234567890abcdef')).toBeInTheDocument();
    });
  });

  describe('Span Tree Rendering', () => {
    it('renders the span tree section', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      expect(screen.getByText('Span Tree')).toBeInTheDocument();
    });

    it('renders all spans in the tree', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      // Use getAllByText since 'api-gateway' appears in both span tree and metric card
      expect(spanTree().getAllByText('api-gateway').length).toBeGreaterThanOrEqual(1);
      expect(spanTree().getByText('user-service')).toBeInTheDocument();
      expect(spanTree().getByText('cache-service')).toBeInTheDocument();
      expect(spanTree().getByText('database')).toBeInTheDocument();
    });

    it('renders operation names for each span', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      expect(spanTree().getByText('handleRequest')).toBeInTheDocument();
      expect(spanTree().getByText('getUser')).toBeInTheDocument();
      expect(spanTree().getByText('getFromCache')).toBeInTheDocument();
      expect(spanTree().getByText('query')).toBeInTheDocument();
    });

    it('renders span IDs in the tree', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      expect(screen.getByText('span-root-1')).toBeInTheDocument();
      expect(screen.getByText('span-child-1')).toBeInTheDocument();
      expect(screen.getByText('span-child-2')).toBeInTheDocument();
      expect(screen.getByText('span-grandchild-1')).toBeInTheDocument();
    });

    it('shows hierarchical structure with indentation', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      // Check that nested spans exist - the tree structure is visible via border styling
      const spanContainers = document.querySelectorAll('.border-l-2');
      expect(spanContainers.length).toBeGreaterThan(0);
    });

    it('handles empty spans array gracefully', () => {
      const emptySpansTrace = { ...mockTrace, spans: [] };
      render(<TraceDetailView trace={emptySpansTrace} onClose={mockOnClose} />);
      expect(screen.getByText('Span Tree')).toBeInTheDocument();
      // Should not crash
    });

    it('handles undefined spans gracefully', () => {
      const noSpansTrace = { ...mockTrace, spans: undefined } as any;
      render(<TraceDetailView trace={noSpansTrace} onClose={mockOnClose} />);
      expect(screen.getByText('Span Tree')).toBeInTheDocument();
      // Should not crash
    });
  });

  describe('Duration Display', () => {
    it('displays total duration in milliseconds', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      // 150000 microseconds = 150ms - appears in both metric card and span
      const durationTexts = screen.getAllByText('150.00ms');
      expect(durationTexts.length).toBeGreaterThanOrEqual(1);
    });

    it('displays "Duration" label', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      expect(screen.getByText('Duration')).toBeInTheDocument();
    });

    it('displays individual span durations', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      // Check some individual span durations
      expect(spanTree().getByText('80.00ms')).toBeInTheDocument(); // user-service span
      expect(spanTree().getByText('5.00ms')).toBeInTheDocument(); // cache-service span
      expect(spanTree().getByText('50.00ms')).toBeInTheDocument(); // database span
    });

    it('handles very small durations', () => {
      const smallDurationTrace = {
        ...mockTrace,
        total_duration_us: 100, // 0.1ms
      };
      render(<TraceDetailView trace={smallDurationTrace} onClose={mockOnClose} />);
      expect(screen.getByText('0.10ms')).toBeInTheDocument();
    });

    it('handles very large durations', () => {
      const largeDurationTrace = {
        ...mockTrace,
        total_duration_us: 5000000, // 5000ms = 5s
      };
      render(<TraceDetailView trace={largeDurationTrace} onClose={mockOnClose} />);
      expect(screen.getByText('5000.00ms')).toBeInTheDocument();
    });
  });

  describe('Back Button', () => {
    it('renders the back button', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      const backButton = screen.getByText(/Back/);
      expect(backButton).toBeInTheDocument();
    });

    it('back button has arrow indicator', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      expect(screen.getByText(/← Back/)).toBeInTheDocument();
    });

    it('calls onClose when back button is clicked', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      const backButton = screen.getByText(/← Back/);
      fireEvent.click(backButton);
      expect(mockOnClose).toHaveBeenCalledTimes(1);
    });

    it('back button has correct styling', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      const backButton = screen.getByText(/← Back/);
      expect(backButton).toHaveClass('bg-amber-500');
    });

    it('back button is a button element', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      const backButton = screen.getByText(/← Back/);
      expect(backButton.tagName).toBe('BUTTON');
    });
  });

  describe('Error Spans Highlighting', () => {
    it('displays ERROR status for error spans', () => {
      render(<TraceDetailView trace={mockTraceWithError} onClose={mockOnClose} />);
      const errorBadges = screen.getAllByText('ERROR');
      expect(errorBadges.length).toBeGreaterThan(0);
    });

    it('displays OK status for successful spans', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      const okBadges = spanTree().getAllByText('OK');
      expect(okBadges.length).toBe(4); // All 4 spans are OK
    });

    it('error spans have red styling', () => {
      render(<TraceDetailView trace={mockTraceWithError} onClose={mockOnClose} />);
      const errorBadge = spanTree().getAllByText('ERROR')[0];
      expect(errorBadge).toHaveClass('text-red-400');
      expect(errorBadge).toHaveClass('bg-red-500/20');
    });

    it('ok spans have green styling', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      const okBadge = spanTree().getAllByText('OK')[0];
      expect(okBadge).toHaveClass('text-emerald-400');
      expect(okBadge).toHaveClass('bg-emerald-500/20');
    });

    it('error span displays error message in attributes', () => {
      render(<TraceDetailView trace={mockTraceWithError} onClose={mockOnClose} />);
      expect(spanTree().getByText('payment-service')).toBeInTheDocument();
      expect(spanTree().getByText('processPayment')).toBeInTheDocument();
    });
  });

  describe('Span Attributes', () => {
    it('renders span attributes when present', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      // Check for some attribute key-value pairs
      expect(screen.getByText('http.method=GET')).toBeInTheDocument();
    });

    it('limits displayed attributes to 5', () => {
      const manyAttributesSpan: SpanUI = {
        span_id: 'span-many-attrs',
        parent_span_id: '',
        service_name: 'test-service',
        operation_name: 'testOp',
        start_time: 1705315800000000,
        duration_us: 10000,
        status: 'OK',
        attributes: {
          attr1: 'value1',
          attr2: 'value2',
          attr3: 'value3',
          attr4: 'value4',
          attr5: 'value5',
          attr6: 'value6', // This should not be displayed
          attr7: 'value7', // This should not be displayed
        },
      };
      const traceWithManyAttrs = { ...mockTrace, spans: [manyAttributesSpan] };
      render(<TraceDetailView trace={traceWithManyAttrs} onClose={mockOnClose} />);

      // First 5 should be present
      expect(screen.getByText('attr1=value1')).toBeInTheDocument();
      expect(screen.getByText('attr5=value5')).toBeInTheDocument();
      // 6th and 7th should not be displayed
      expect(screen.queryByText('attr6=value6')).not.toBeInTheDocument();
      expect(screen.queryByText('attr7=value7')).not.toBeInTheDocument();
    });

    it('handles spans with no attributes', () => {
      const noAttributesSpan: SpanUI = {
        span_id: 'span-no-attrs',
        parent_span_id: '',
        service_name: 'test-service',
        operation_name: 'testOp',
        start_time: 1705315800000000,
        duration_us: 10000,
        status: 'OK',
        attributes: {},
      };
      const traceWithNoAttrs = { ...mockTrace, spans: [noAttributesSpan] };
      render(<TraceDetailView trace={traceWithNoAttrs} onClose={mockOnClose} />);
      expect(spanTree().getByText('test-service')).toBeInTheDocument();
      // Should not crash and should not show attribute section
    });

    it('handles spans with undefined attributes', () => {
      const undefinedAttributesSpan: SpanUI = {
        span_id: 'span-undefined-attrs',
        parent_span_id: '',
        service_name: 'test-service',
        operation_name: 'testOp',
        start_time: 1705315800000000,
        duration_us: 10000,
        status: 'OK',
        attributes: undefined as any,
      };
      const traceWithUndefinedAttrs = { ...mockTrace, spans: [undefinedAttributesSpan] };
      render(<TraceDetailView trace={traceWithUndefinedAttrs} onClose={mockOnClose} />);
      expect(spanTree().getByText('test-service')).toBeInTheDocument();
      // Should not crash
    });
  });

  describe('Metric Cards', () => {
    it('displays Service metric card', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      expect(screen.getByText('Service')).toBeInTheDocument();
    });

    it('displays service name in metric card', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      // Service ID is split by colon and first part is shown - appears multiple times
      const apiGatewayTexts = screen.getAllByText('api-gateway');
      expect(apiGatewayTexts.length).toBeGreaterThanOrEqual(1);
    });

    it('displays Duration metric card', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      expect(screen.getByText('Duration')).toBeInTheDocument();
    });

    it('displays Spans metric card', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      expect(screen.getByText('Spans')).toBeInTheDocument();
      expect(screen.getByText('4')).toBeInTheDocument(); // 4 spans
    });

    it('displays Fingerprint metric card', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      expect(screen.getByText('Fingerprint')).toBeInTheDocument();
      // Shows first 8 characters of fingerprint
      expect(screen.getByText('fp-hash-')).toBeInTheDocument();
    });

    it('handles missing service_id gracefully', () => {
      const noServiceIdTrace = { ...mockTrace, service_id: undefined } as any;
      render(<TraceDetailView trace={noServiceIdTrace} onClose={mockOnClose} />);
      expect(screen.getByText('Service')).toBeInTheDocument();
      expect(metricCard('Service').getByText('-')).toBeInTheDocument();
    });

    it('handles missing fingerprint gracefully', () => {
      const noFingerprintTrace = { ...mockTrace, fingerprint: undefined } as any;
      render(<TraceDetailView trace={noFingerprintTrace} onClose={mockOnClose} />);
      expect(screen.getByText('Fingerprint')).toBeInTheDocument();
      const fingerprintCard = screen.getByText('Fingerprint').closest('div');
      expect(within(fingerprintCard!.parentElement!).getByText('-')).toBeInTheDocument();
    });
  });

  describe('Tree Structure', () => {
    it('builds correct parent-child relationships', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      // Root span should be at top level - appears in metric card and span tree
      expect(spanTree().getAllByText('api-gateway').length).toBeGreaterThanOrEqual(1);
      // Child spans should be nested
      expect(spanTree().getByText('user-service')).toBeInTheDocument();
      expect(spanTree().getByText('cache-service')).toBeInTheDocument();
      // Grandchild span
      expect(spanTree().getByText('database')).toBeInTheDocument();
    });

    it('handles orphan spans (parent not found)', () => {
      const orphanSpan: SpanUI = {
        span_id: 'span-orphan',
        parent_span_id: 'non-existent-parent',
        service_name: 'orphan-service',
        operation_name: 'orphanOp',
        start_time: 1705315800000000,
        duration_us: 10000,
        status: 'OK',
        attributes: {},
      };
      const traceWithOrphan = { ...mockTrace, spans: [...mockSpans, orphanSpan] };
      render(<TraceDetailView trace={traceWithOrphan} onClose={mockOnClose} />);
      // Orphan should be rendered as a root
      expect(spanTree().getByText('orphan-service')).toBeInTheDocument();
    });

    it('handles multiple root spans', () => {
      const secondRootSpan: SpanUI = {
        span_id: 'span-root-2',
        parent_span_id: '',
        service_name: 'second-root-service',
        operation_name: 'secondRootOp',
        start_time: 1705315800000000,
        duration_us: 10000,
        status: 'OK',
        attributes: {},
      };
      const traceWithMultipleRoots = { ...mockTrace, spans: [...mockSpans, secondRootSpan] };
      render(<TraceDetailView trace={traceWithMultipleRoots} onClose={mockOnClose} />);
      // api-gateway appears in metric card and span tree
      expect(spanTree().getAllByText('api-gateway').length).toBeGreaterThanOrEqual(1);
      expect(spanTree().getByText('second-root-service')).toBeInTheDocument();
    });

    it('handles deeply nested spans', () => {
      const deepSpans: SpanUI[] = [
        {
          span_id: 'level-0',
          parent_span_id: '',
          service_name: 'level-0-service',
          operation_name: 'level0',
          start_time: 1705315800000000,
          duration_us: 100000,
          status: 'OK',
          attributes: {},
        },
        {
          span_id: 'level-1',
          parent_span_id: 'level-0',
          service_name: 'level-1-service',
          operation_name: 'level1',
          start_time: 1705315800010000,
          duration_us: 80000,
          status: 'OK',
          attributes: {},
        },
        {
          span_id: 'level-2',
          parent_span_id: 'level-1',
          service_name: 'level-2-service',
          operation_name: 'level2',
          start_time: 1705315800020000,
          duration_us: 60000,
          status: 'OK',
          attributes: {},
        },
        {
          span_id: 'level-3',
          parent_span_id: 'level-2',
          service_name: 'level-3-service',
          operation_name: 'level3',
          start_time: 1705315800030000,
          duration_us: 40000,
          status: 'OK',
          attributes: {},
        },
        {
          span_id: 'level-4',
          parent_span_id: 'level-3',
          service_name: 'level-4-service',
          operation_name: 'level4',
          start_time: 1705315800040000,
          duration_us: 20000,
          status: 'OK',
          attributes: {},
        },
      ];
      const deepTrace = { ...mockTrace, spans: deepSpans };
      render(<TraceDetailView trace={deepTrace} onClose={mockOnClose} />);

      // All levels should be rendered
      expect(spanTree().getByText('level-0-service')).toBeInTheDocument();
      expect(spanTree().getByText('level-1-service')).toBeInTheDocument();
      expect(spanTree().getByText('level-2-service')).toBeInTheDocument();
      expect(spanTree().getByText('level-3-service')).toBeInTheDocument();
      expect(spanTree().getByText('level-4-service')).toBeInTheDocument();
    });
  });

  describe('Timeline Waterfall', () => {
    it('renders a waterfall bar per span between the metric cards and the span tree', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      const waterfall = screen.getByRole('list', { name: 'Trace waterfall timeline' });
      expect(within(waterfall).getAllByTestId('waterfall-bar')).toHaveLength(mockSpans.length);

      const timelineHeading = screen.getByText('Timeline');
      const spanTreeHeading = screen.getByText('Span Tree');
      const metricLabel = screen.getByText('Fingerprint');
      expect(metricLabel.compareDocumentPosition(timelineHeading) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      expect(timelineHeading.compareDocumentPosition(spanTreeHeading) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    });

    it('positions bars from real span start times', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      const [rootBar] = screen.getAllByTestId('waterfall-bar');
      expect(rootBar).toHaveStyle({ left: '0%', width: '100%' });
    });
  });

  describe('Scroll Behavior', () => {
    it('span tree container has max height and scroll', () => {
      render(<TraceDetailView trace={mockTrace} onClose={mockOnClose} />);
      const scrollContainer = document.querySelector('.max-h-\\[500px\\]');
      expect(scrollContainer).toBeInTheDocument();
      expect(scrollContainer).toHaveClass('overflow-y-auto');
    });
  });

  describe('Edge Cases', () => {
    it('handles trace with single span', () => {
      const singleSpanTrace = {
        ...mockTrace,
        spans: [mockSpans[0]],
      };
      render(<TraceDetailView trace={singleSpanTrace} onClose={mockOnClose} />);
      // api-gateway appears in metric card and span tree
      expect(screen.getAllByText('api-gateway').length).toBeGreaterThanOrEqual(1);
      expect(screen.getByText('1')).toBeInTheDocument(); // 1 span
    });

    it('handles span with special characters in operation name', () => {
      const specialCharSpan: SpanUI = {
        span_id: 'special-span',
        parent_span_id: '',
        service_name: 'test-service',
        operation_name: 'GET /api/users/{id}',
        start_time: 1705315800000000,
        duration_us: 10000,
        status: 'OK',
        attributes: {},
      };
      const traceWithSpecialChar = { ...mockTrace, spans: [specialCharSpan] };
      render(<TraceDetailView trace={traceWithSpecialChar} onClose={mockOnClose} />);
      expect(spanTree().getByText('GET /api/users/{id}')).toBeInTheDocument();
    });

    it('handles service_id without colon separator', () => {
      const noColonTrace = {
        ...mockTrace,
        service_id: 'simple-service-id',
      };
      render(<TraceDetailView trace={noColonTrace} onClose={mockOnClose} />);
      expect(screen.getByText('simple-service-id')).toBeInTheDocument();
    });

    it('handles zero duration', () => {
      const zeroDurationTrace = {
        ...mockTrace,
        total_duration_us: 0,
      };
      render(<TraceDetailView trace={zeroDurationTrace} onClose={mockOnClose} />);
      expect(metricCard('Duration').getByText('0.00ms')).toBeInTheDocument();
    });
  });
});
