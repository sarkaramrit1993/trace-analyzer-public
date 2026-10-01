import { render, screen, fireEvent, within } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { TraceWaterfall } from './TraceWaterfall';
import type { TraceDetail, SpanUI, ServiceGrouping } from '../../types';

// Mock service grouping
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

/**
 * Helper to create a mock trace
 */
function createMockTrace(spans: SpanUI[]): TraceDetail {
  return {
    trace_id: 'trace-123',
    service_id: 'test-service:test-op',
    service_grouping: mockServiceGrouping,
    fingerprint: 'fp-12345678',
    total_duration_us: 150000,
    has_error: false,
    spans,
  };
}

// Sample spans for testing
const sampleSpans: SpanUI[] = [
  createSpanUI({
    span_id: 'span-root',
    parent_span_id: '',
    service_name: 'api-gateway',
    operation_name: 'handleRequest',
    start_time: 1000000,
    duration_us: 150000,
    status: 'OK',
    attributes: { 'http.method': 'GET', 'http.url': '/api/users' },
  }),
  createSpanUI({
    span_id: 'span-auth',
    parent_span_id: 'span-root',
    service_name: 'auth-service',
    operation_name: 'validateToken',
    start_time: 1010000,
    duration_us: 5000,
    status: 'OK',
    attributes: {},
  }),
  createSpanUI({
    span_id: 'span-user',
    parent_span_id: 'span-root',
    service_name: 'user-service',
    operation_name: 'getUser',
    start_time: 1020000,
    duration_us: 80000,
    status: 'OK',
    attributes: { 'db.type': 'postgres' },
  }),
  createSpanUI({
    span_id: 'span-db',
    parent_span_id: 'span-user',
    service_name: 'postgres',
    operation_name: 'SELECT',
    start_time: 1030000,
    duration_us: 50000,
    status: 'OK',
    attributes: { 'db.statement': 'SELECT * FROM users' },
  }),
];

const sampleTrace = createMockTrace(sampleSpans);

describe('TraceWaterfall', () => {
  describe('Basic Rendering', () => {
    it('renders waterfall with spans', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      // All service names should be visible
      expect(screen.getByText('api-gateway')).toBeInTheDocument();
      expect(screen.getByText('auth-service')).toBeInTheDocument();
      expect(screen.getByText('user-service')).toBeInTheDocument();
      expect(screen.getByText('postgres')).toBeInTheDocument();
    });

    it('renders operation names', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      expect(screen.getByText('handleRequest')).toBeInTheDocument();
      expect(screen.getByText('validateToken')).toBeInTheDocument();
      expect(screen.getByText('getUser')).toBeInTheDocument();
      expect(screen.getByText('SELECT')).toBeInTheDocument();
    });

    it('renders legend with color indicators', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      expect(screen.getByText('OK')).toBeInTheDocument();
      expect(screen.getByText('ERROR')).toBeInTheDocument();
      expect(screen.getByText('Critical Path')).toBeInTheDocument();
    });

    it('displays span count in legend', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      expect(screen.getByText(/4 spans/)).toBeInTheDocument();
    });

    it('has proper accessibility role', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      const list = screen.getByRole('list', { name: /trace waterfall timeline/i });
      expect(list).toBeInTheDocument();
    });
  });

  describe('Empty State', () => {
    it('shows message when no spans', () => {
      const emptyTrace = createMockTrace([]);
      render(<TraceWaterfall trace={emptyTrace} />);

      expect(screen.getByText('No spans to display')).toBeInTheDocument();
    });

    it('shows message when spans is undefined', () => {
      const noSpansTrace = { ...sampleTrace, spans: undefined } as any;
      render(<TraceWaterfall trace={noSpansTrace} />);

      expect(screen.getByText('No spans to display')).toBeInTheDocument();
    });
  });

  describe('Timeline Bars', () => {
    it('renders timeline bars for each span', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      const bars = screen.getAllByTestId('waterfall-bar');
      expect(bars.length).toBe(4);
    });

    it('applies correct color for OK spans', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      // OK spans should have emerald color class
      const bars = screen.getAllByTestId('waterfall-bar');
      // At least one bar should have emerald (OK) or amber (critical path) color
      const hasCorrectColor = bars.some(
        bar =>
          bar.className.includes('bg-emerald-500') || bar.className.includes('bg-amber-500')
      );
      expect(hasCorrectColor).toBe(true);
    });

    it('applies correct color for ERROR spans', () => {
      // Create a trace where the error span is NOT on the critical path
      // Root -> longerChild (OK, longer duration - on critical path)
      // Root -> errorChild (ERROR, shorter duration - NOT on critical path, should be red)
      const spans = [
        createSpanUI({
          span_id: 'root',
          parent_span_id: '',
          service_name: 'root-service',
          operation_name: 'rootOp',
          start_time: 1000000,
          duration_us: 100000,
          status: 'OK',
        }),
        createSpanUI({
          span_id: 'longer-child',
          parent_span_id: 'root',
          service_name: 'ok-service',
          operation_name: 'okOp',
          start_time: 1010000,
          duration_us: 50000, // Longer, so it's on critical path
          status: 'OK',
        }),
        createSpanUI({
          span_id: 'error-child',
          parent_span_id: 'root',
          service_name: 'failing-service',
          operation_name: 'failOp',
          start_time: 1020000,
          duration_us: 10000, // Shorter, so NOT on critical path
          status: 'ERROR',
        }),
      ];
      const errorTrace = createMockTrace(spans);

      render(<TraceWaterfall trace={errorTrace} />);

      const bars = screen.getAllByTestId('waterfall-bar');
      const hasRedColor = bars.some(bar => bar.className.includes('bg-red-500'));
      expect(hasRedColor).toBe(true);
    });

    it('applies amber color for critical path spans', () => {
      // With single span trace, that span is always on critical path
      const singleSpan = createSpanUI({
        span_id: 'critical-span',
        parent_span_id: '',
        duration_us: 100000,
      });
      const singleTrace = createMockTrace([singleSpan]);

      render(<TraceWaterfall trace={singleTrace} />);

      const bars = screen.getAllByTestId('waterfall-bar');
      const hasAmberColor = bars.some(bar => bar.className.includes('bg-amber-500'));
      expect(hasAmberColor).toBe(true);
    });
  });

  describe('Duration Display', () => {
    it('displays duration for wide bars', () => {
      // Single span takes 100% width, so duration should show inside
      const wideSpan = createSpanUI({
        span_id: 'wide-span',
        duration_us: 100000, // 100ms
      });
      const wideTrace = createMockTrace([wideSpan]);

      render(<TraceWaterfall trace={wideTrace} />);

      // Duration appears in both TimeAxis and the bar - just verify it exists
      const durationElements = screen.getAllByText('100.00ms');
      expect(durationElements.length).toBeGreaterThan(0);
    });

    it('formats durations correctly', () => {
      const spans = [
        createSpanUI({ span_id: 'ms-span', duration_us: 1500 }), // 1.5ms
      ];
      const trace = createMockTrace(spans);

      render(<TraceWaterfall trace={trace} />);

      // Duration appears in both TimeAxis and the bar - just verify it exists
      const durationElements = screen.getAllByText('1.50ms');
      expect(durationElements.length).toBeGreaterThan(0);
    });
  });

  describe('Span Selection', () => {
    it('calls onSpanClick when span row is clicked', () => {
      const onSpanClick = vi.fn();
      render(<TraceWaterfall trace={sampleTrace} onSpanClick={onSpanClick} />);

      const spanRows = screen.getAllByRole('listitem');
      fireEvent.click(spanRows[0]);

      expect(onSpanClick).toHaveBeenCalledTimes(1);
      expect(onSpanClick).toHaveBeenCalledWith(expect.objectContaining({
        span_id: expect.any(String),
      }));
    });

    it('shows detail panel when span is selected', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      const spanRows = screen.getAllByRole('listitem');
      fireEvent.click(spanRows[0]);

      // Detail panel should appear with span details
      expect(screen.getByText('Duration')).toBeInTheDocument();
      expect(screen.getByText('Status')).toBeInTheDocument();
      expect(screen.getByText('Span ID')).toBeInTheDocument();
      expect(screen.getByText('Parent ID')).toBeInTheDocument();
    });

    it('hides detail panel when clicking same span again', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      const spanRows = screen.getAllByRole('listitem');

      // Click to select
      fireEvent.click(spanRows[0]);
      expect(screen.getByText('Span ID')).toBeInTheDocument();

      // Click again to deselect
      fireEvent.click(spanRows[0]);
      expect(screen.queryByText('Span ID')).not.toBeInTheDocument();
    });

    it('shows close button in detail panel', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      const spanRows = screen.getAllByRole('listitem');
      fireEvent.click(spanRows[0]);

      const closeButton = screen.getByLabelText('Close span details');
      expect(closeButton).toBeInTheDocument();

      fireEvent.click(closeButton);
      expect(screen.queryByText('Span ID')).not.toBeInTheDocument();
    });

    it('highlights selected span row', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      const spanRows = screen.getAllByRole('listitem');
      fireEvent.click(spanRows[0]);

      expect(spanRows[0]).toHaveClass('bg-slate-700/50');
    });
  });

  describe('Keyboard Navigation', () => {
    it('selects span on Enter key', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      const spanRows = screen.getAllByRole('listitem');
      spanRows[0].focus();
      fireEvent.keyDown(spanRows[0], { key: 'Enter' });

      expect(screen.getByText('Span ID')).toBeInTheDocument();
    });

    it('selects span on Space key', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      const spanRows = screen.getAllByRole('listitem');
      spanRows[0].focus();
      fireEvent.keyDown(spanRows[0], { key: ' ' });

      expect(screen.getByText('Span ID')).toBeInTheDocument();
    });

    it('span rows are focusable', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      const spanRows = screen.getAllByRole('listitem');
      spanRows.forEach(row => {
        expect(row).toHaveAttribute('tabindex', '0');
      });
    });
  });

  describe('Span Attributes', () => {
    it('displays attributes in detail panel', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      // Click on the root span which has http.method and http.url attributes
      const spanRows = screen.getAllByRole('listitem');
      // Find and click the api-gateway span
      const gatewayRow = spanRows.find(row =>
        row.textContent?.includes('api-gateway')
      );
      expect(gatewayRow).toBeDefined();
      fireEvent.click(gatewayRow!);

      expect(screen.getByText('Attributes')).toBeInTheDocument();
      expect(screen.getByText(/http\.method/)).toBeInTheDocument();
    });

    it('handles spans with no attributes', () => {
      const noAttrSpan = createSpanUI({
        span_id: 'no-attr',
        attributes: {},
      });
      const trace = createMockTrace([noAttrSpan]);

      render(<TraceWaterfall trace={trace} />);

      const spanRows = screen.getAllByRole('listitem');
      fireEvent.click(spanRows[0]);

      // Should not show attributes section
      expect(screen.queryByText('Attributes')).not.toBeInTheDocument();
    });
  });

  describe('Critical Path Highlighting', () => {
    it('indicates critical path spans in legend', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      // The legend shows "X on critical path"
      expect(screen.getByText(/on critical path/)).toBeInTheDocument();
    });

    it('includes critical path info in span aria-label', () => {
      // Single span is always on critical path
      const singleSpan = createSpanUI({
        span_id: 'critical',
        service_name: 'critical-service',
        operation_name: 'criticalOp',
        duration_us: 50000,
      });
      const trace = createMockTrace([singleSpan]);

      render(<TraceWaterfall trace={trace} />);

      const spanRow = screen.getByRole('listitem');
      expect(spanRow).toHaveAttribute(
        'aria-label',
        expect.stringContaining('on critical path')
      );
    });
  });

  describe('Depth/Indentation', () => {
    it('applies indentation based on span depth', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      const spanRows = screen.getAllByRole('listitem');

      // Root span (depth 0) should have no padding
      const rootRow = spanRows.find(row => row.textContent?.includes('api-gateway'));
      const rootLabel = rootRow?.querySelector('div[style]');
      expect(rootLabel).toHaveStyle({ paddingLeft: '0px' });

      // Child span (depth 1) should have 16px padding
      const childRow = spanRows.find(row => row.textContent?.includes('auth-service'));
      const childLabel = childRow?.querySelector('div[style]');
      expect(childLabel).toHaveStyle({ paddingLeft: '16px' });

      // Grandchild span (depth 2) should have 32px padding
      const grandchildRow = spanRows.find(row => row.textContent?.includes('postgres'));
      const grandchildLabel = grandchildRow?.querySelector('div[style]');
      expect(grandchildLabel).toHaveStyle({ paddingLeft: '32px' });
    });
  });

  describe('Error Status', () => {
    it('includes error info in span aria-label for error spans', () => {
      const errorSpan = createSpanUI({
        span_id: 'error-span',
        service_name: 'error-service',
        status: 'ERROR',
      });
      const trace = createMockTrace([errorSpan]);

      render(<TraceWaterfall trace={trace} />);

      const spanRow = screen.getByRole('listitem');
      expect(spanRow).toHaveAttribute(
        'aria-label',
        expect.stringContaining('has error')
      );
    });

    it('shows ERROR status in detail panel', () => {
      const errorSpan = createSpanUI({
        span_id: 'error-span',
        status: 'ERROR',
      });
      const trace = createMockTrace([errorSpan]);

      render(<TraceWaterfall trace={trace} />);

      fireEvent.click(screen.getByRole('listitem'));

      // Status section should show ERROR with red color
      const statusSection = screen.getByText('Status').parentElement;
      expect(within(statusSection!).getByText('ERROR')).toHaveClass('text-red-400');
    });
  });

  describe('Time Axis', () => {
    it('renders time axis component', () => {
      render(<TraceWaterfall trace={sampleTrace} />);

      // Time axis should show at least 0ms - may appear multiple times
      const zeroMsElements = screen.getAllByText('0.00ms');
      expect(zeroMsElements.length).toBeGreaterThan(0);
    });
  });

  describe('Edge Cases', () => {
    it('handles single span trace', () => {
      const singleSpan = createSpanUI({ span_id: 'only-span' });
      const trace = createMockTrace([singleSpan]);

      render(<TraceWaterfall trace={trace} />);

      expect(screen.getByText('test-service')).toBeInTheDocument();
      expect(screen.getByText(/1 spans/)).toBeInTheDocument();
    });

    it('handles spans with zero duration', () => {
      const zeroSpan = createSpanUI({
        span_id: 'zero-span',
        duration_us: 0,
      });
      const trace = createMockTrace([zeroSpan]);

      render(<TraceWaterfall trace={trace} />);

      // Duration appears in TimeAxis and possibly in the bar
      const zeroMsElements = screen.getAllByText('0.00ms');
      expect(zeroMsElements.length).toBeGreaterThan(0);
    });

    it('handles very long service names', () => {
      const longNameSpan = createSpanUI({
        span_id: 'long-name-span',
        service_name: 'very-long-service-name-that-should-be-truncated',
        operation_name: 'alsoVeryLongOperationNameThatShouldBeTruncated',
      });
      const trace = createMockTrace([longNameSpan]);

      render(<TraceWaterfall trace={trace} />);

      // Should render without crashing and apply truncation
      expect(screen.getByText('very-long-service-name-that-should-be-truncated')).toBeInTheDocument();
    });

    it('passes custom className to container', () => {
      const { container } = render(
        <TraceWaterfall trace={sampleTrace} className="custom-class" />
      );

      const wrapper = container.firstChild;
      expect(wrapper).toHaveClass('custom-class');
    });
  });
});
