import { useMemo, useState } from 'react';
import type { TraceDetail, SpanUI } from '../../types';
import { computeCriticalPathUI } from '../../utils/criticalPath';
import {
  computeSpanUIDepth,
  computeSpanUITiming,
  formatDuration,
  getTraceDurationFromUI,
} from '../../utils/traceUtils';
import {
  sanitizeDuration,
  sanitizeStatus,
  sanitizeAttributes,
  DEFAULTS,
} from '../../utils/spanValidation';
import { TimeAxis } from './TimeAxis';

/**
 * Extended span type with computed waterfall properties
 */
interface WaterfallSpan {
  span: SpanUI;
  /** Percentage offset from trace start (0-100) */
  offsetPercent: number;
  /** Percentage width relative to total duration (0-100) */
  widthPercent: number;
  /** Nesting depth (0 for root) */
  depth: number;
  /** Whether this span is on the critical path */
  isCriticalPath: boolean;
}

interface TraceWaterfallProps {
  /** The trace to visualize */
  trace: TraceDetail;
  /** Callback when a span is clicked */
  onSpanClick?: (span: SpanUI) => void;
  /** Optional className for the container */
  className?: string;
}

interface WaterfallRowProps {
  span: SpanUI;
  offsetPercent: number;
  widthPercent: number;
  depth: number;
  isCriticalPath: boolean;
  isSelected: boolean;
  onClick: () => void;
}

/**
 * Individual row in the waterfall, representing one span
 */
function WaterfallRow({
  span,
  offsetPercent,
  widthPercent,
  depth,
  isCriticalPath,
  isSelected,
  onClick,
}: WaterfallRowProps) {
  // Sanitize span data with fallbacks
  const serviceName = span.service_name || DEFAULTS.SERVICE;
  const operationName = span.operation_name || DEFAULTS.OPERATION;
  const status = sanitizeStatus(span.status);
  const durationUs = sanitizeDuration(span.duration_us);

  // Ensure percentages are finite numbers
  const safeOffsetPercent = Number.isFinite(offsetPercent) ? offsetPercent : 0;
  const safeWidthPercent = Number.isFinite(widthPercent) ? widthPercent : 0;

  const barColorClass = useMemo(() => {
    if (isCriticalPath) {
      return 'bg-amber-500 ring-2 ring-amber-400/50';
    }
    return status === 'OK' ? 'bg-emerald-500' : 'bg-red-500';
  }, [isCriticalPath, status]);

  // Calculate indentation based on depth (16px per level)
  const paddingLeft = depth * 16;

  return (
    <div
      className={`flex items-center h-8 group cursor-pointer transition-colors ${
        isSelected ? 'bg-slate-700/50' : 'hover:bg-slate-800/50'
      }`}
      onClick={onClick}
      role="listitem"
      aria-label={`${serviceName} ${operationName}, duration ${formatDuration(durationUs)}${
        isCriticalPath ? ', on critical path' : ''
      }${status === 'ERROR' ? ', has error' : ''}`}
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onClick();
        }
      }}
    >
      {/* Service/Operation labels */}
      <div
        className="w-64 flex-shrink-0 truncate pr-2"
        style={{ paddingLeft: `${paddingLeft}px` }}
        title={`${serviceName} - ${operationName}`}
      >
        <span className="text-emerald-400 text-sm font-medium">{serviceName}</span>
        <span className="text-slate-500 mx-1">-</span>
        <span className="text-slate-300 text-sm">{operationName}</span>
      </div>

      {/* Timeline bar container */}
      <div className="flex-1 relative h-6 bg-slate-800/30 rounded">
        {/* The span bar */}
        <div
          className={`absolute h-full rounded transition-all ${barColorClass} group-hover:brightness-110`}
          style={{
            left: `${safeOffsetPercent}%`,
            width: `${Math.max(safeWidthPercent, 0.5)}%`,
          }}
          data-testid="waterfall-bar"
        >
          {/* Duration label on the bar (only if wide enough) */}
          {safeWidthPercent > 8 && (
            <span className="absolute right-1 top-1/2 -translate-y-1/2 text-xs text-white font-medium">
              {formatDuration(durationUs)}
            </span>
          )}
        </div>

        {/* Duration label outside the bar (if bar is too narrow) */}
        {safeWidthPercent <= 8 && (
          <span
            className="absolute top-1/2 -translate-y-1/2 text-xs text-slate-400 whitespace-nowrap"
            style={{ left: `calc(${safeOffsetPercent + safeWidthPercent}% + 4px)` }}
          >
            {formatDuration(durationUs)}
          </span>
        )}
      </div>
    </div>
  );
}

/**
 * Span detail panel shown when a span is selected
 */
function SpanDetailPanel({ span, onClose }: { span: SpanUI; onClose: () => void }) {
  // Sanitize span data with fallbacks
  const serviceName = span.service_name || DEFAULTS.SERVICE;
  const operationName = span.operation_name || DEFAULTS.OPERATION;
  const status = sanitizeStatus(span.status);
  const durationUs = sanitizeDuration(span.duration_us);
  const attributes = sanitizeAttributes(span.attributes);
  const spanId = span.span_id || 'unknown';
  const parentSpanId = span.parent_span_id || 'None (root)';

  return (
    <div className="mt-4 bg-slate-800/50 rounded-lg p-4 border border-slate-700/50">
      <div className="flex justify-between items-start mb-3">
        <div>
          <h4 className="text-emerald-400 font-medium">{serviceName}</h4>
          <p className="text-slate-300 text-sm">{operationName}</p>
        </div>
        <button
          onClick={onClose}
          className="text-slate-500 hover:text-slate-300 transition-colors"
          aria-label="Close span details"
        >
          <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
        <div>
          <span className="text-slate-500 text-xs uppercase">Duration</span>
          <p className="text-slate-200 font-medium">{formatDuration(durationUs)}</p>
        </div>
        <div>
          <span className="text-slate-500 text-xs uppercase">Status</span>
          <p className={`font-medium ${status === 'OK' ? 'text-emerald-400' : 'text-red-400'}`}>
            {status}
          </p>
        </div>
        <div>
          <span className="text-slate-500 text-xs uppercase">Span ID</span>
          <p className="text-slate-200 font-mono text-xs truncate" title={spanId}>
            {spanId}
          </p>
        </div>
        <div>
          <span className="text-slate-500 text-xs uppercase">Parent ID</span>
          <p className="text-slate-200 font-mono text-xs truncate" title={parentSpanId}>
            {parentSpanId}
          </p>
        </div>
      </div>

      {/* Attributes */}
      {Object.keys(attributes).length > 0 && (
        <div>
          <span className="text-slate-500 text-xs uppercase block mb-2">Attributes</span>
          <div className="flex flex-wrap gap-2">
            {Object.entries(attributes).map(([key, value]) => (
              <span
                key={key}
                className="bg-slate-700/50 px-2 py-1 rounded text-xs text-slate-300"
                title={`${key}=${value}`}
              >
                <span className="text-slate-500">{key}=</span>
                {value}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

/**
 * TraceWaterfall displays a timeline visualization of trace spans.
 *
 * Features:
 * - Timeline bars positioned by start time and sized by duration
 * - Color coding: green for OK, red for ERROR, amber for critical path
 * - Service name and operation displayed on the left
 * - Duration labels on the bars
 * - Click to expand span details
 *
 * @example
 * ```tsx
 * <TraceWaterfall
 *   trace={traceDetail}
 *   onSpanClick={(span) => console.log('Clicked:', span.span_id)}
 * />
 * ```
 */
export function TraceWaterfall({ trace, onSpanClick, className = '' }: TraceWaterfallProps) {
  const [selectedSpanId, setSelectedSpanId] = useState<string | null>(null);

  const { waterfallSpans, traceDuration, criticalPath } = useMemo(() => {
    // Guard against null/undefined trace.spans
    const spans = trace.spans ?? [];

    if (spans.length === 0) {
      return {
        waterfallSpans: [],
        traceDuration: 0,
        criticalPath: new Set<string>(),
      };
    }

    // Sort spans by start time
    const sortedSpans = [...spans].sort((a, b) => a.start_time - b.start_time);

    // Compute critical path
    const criticalPathSet = computeCriticalPathUI(sortedSpans);

    // Compute timing info
    const timingMap = computeSpanUITiming(sortedSpans);

    // Compute total duration
    const totalDuration = getTraceDurationFromUI(sortedSpans);

    // Build waterfall spans with all computed properties
    const waterfall: WaterfallSpan[] = sortedSpans.map((span) => {
      const timing = timingMap.get(span.span_id) || { offsetPercent: 0, widthPercent: 0 };
      return {
        span,
        offsetPercent: timing.offsetPercent,
        widthPercent: timing.widthPercent,
        depth: computeSpanUIDepth(span, sortedSpans),
        isCriticalPath: criticalPathSet.has(span.span_id),
      };
    });

    return {
      waterfallSpans: waterfall,
      traceDuration: totalDuration,
      criticalPath: criticalPathSet,
    };
  }, [trace.spans]);

  const selectedSpan = useMemo(() => {
    if (!selectedSpanId) return null;
    return trace.spans?.find((s) => s.span_id === selectedSpanId) || null;
  }, [selectedSpanId, trace.spans]);

  const handleSpanClick = (span: SpanUI) => {
    setSelectedSpanId((prev) => (prev === span.span_id ? null : span.span_id));
    onSpanClick?.(span);
  };

  if (!trace.spans || trace.spans.length === 0) {
    return (
      <div className={`text-slate-500 text-center py-8 ${className}`}>
        No spans to display
      </div>
    );
  }

  return (
    <div className={`${className}`} role="list" aria-label="Trace waterfall timeline">
      {/* Legend */}
      <div className="flex items-center gap-4 mb-4 text-xs text-slate-400">
        <div className="flex items-center gap-1">
          <div className="w-3 h-3 rounded bg-emerald-500" />
          <span>OK</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="w-3 h-3 rounded bg-red-500" />
          <span>ERROR</span>
        </div>
        <div className="flex items-center gap-1">
          <div className="w-3 h-3 rounded bg-amber-500 ring-2 ring-amber-400/50" />
          <span>Critical Path</span>
        </div>
        <div className="ml-auto text-slate-500">
          {trace.spans.length} spans | {criticalPath.size} on critical path
        </div>
      </div>

      {/* Time axis */}
      <TimeAxis durationUs={traceDuration} />

      {/* Waterfall rows */}
      <div className="space-y-1">
        {waterfallSpans.map(({ span, offsetPercent, widthPercent, depth, isCriticalPath }) => (
          <WaterfallRow
            key={span.span_id}
            span={span}
            offsetPercent={offsetPercent}
            widthPercent={widthPercent}
            depth={depth}
            isCriticalPath={isCriticalPath}
            isSelected={span.span_id === selectedSpanId}
            onClick={() => handleSpanClick(span)}
          />
        ))}
      </div>

      {/* Selected span detail panel */}
      {selectedSpan && (
        <SpanDetailPanel
          span={selectedSpan}
          onClose={() => setSelectedSpanId(null)}
        />
      )}
    </div>
  );
}
