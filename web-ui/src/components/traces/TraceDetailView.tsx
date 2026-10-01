import { Card, MetricCard } from '../common';
import type { TraceDetail, SpanUI } from '../../types';
import {
  MAX_SPAN_DEPTH,
  isValidSpanUI,
  sanitizeStatus,
  sanitizeAttributes,
  DEFAULTS,
  warnMalformedData,
} from '../../utils/spanValidation';
import { TraceWaterfall } from './TraceWaterfall';

/**
 * Extended span type that includes children for tree representation
 */
interface SpanTreeNode extends SpanUI {
  children: SpanTreeNode[];
}

interface TraceDetailViewProps {
  trace: TraceDetail;
  onClose: () => void;
}

interface SpanNodeProps {
  span: SpanTreeNode;
  level: number;
  maxDepth?: number;
}

/**
 * Converts a flat array of spans into a tree structure based on parent-child relationships
 */
function buildSpanTree(spans: SpanUI[]): SpanTreeNode[] {
  // Filter out invalid spans (missing span_id) and handle null/undefined spans array
  const validSpans = (spans || []).filter(s => {
    if (!isValidSpanUI(s)) {
      warnMalformedData('buildSpanTree', `Invalid span detected: ${JSON.stringify(s)}`);
      return false;
    }
    return true;
  });

  // Guard against duplicate span_ids
  const seenSpanIds = new Set<string>();
  const uniqueSpans = validSpans.filter(s => {
    if (seenSpanIds.has(s.span_id)) {
      warnMalformedData('buildSpanTree', `Duplicate span_id detected: ${s.span_id}`);
      return false;
    }
    seenSpanIds.add(s.span_id);
    return true;
  });

  const spanMap: Record<string, SpanTreeNode> = {};

  // Create tree nodes with empty children arrays
  uniqueSpans.forEach(s => {
    spanMap[s.span_id] = { ...s, children: [] };
  });

  const roots: SpanTreeNode[] = [];

  // Build the tree by linking children to parents
  uniqueSpans.forEach(s => {
    if (s.parent_span_id && spanMap[s.parent_span_id]) {
      // Cycle detection: Check if adding this child would create a cycle
      let current: SpanTreeNode | undefined = spanMap[s.parent_span_id];
      let depth = 0;
      const visited = new Set<string>();

      while (current && depth < MAX_SPAN_DEPTH) {
        if (visited.has(current.span_id)) {
          warnMalformedData('buildSpanTree', `Cycle detected involving span ${s.span_id}`);
          roots.push(spanMap[s.span_id]);
          return;
        }
        visited.add(current.span_id);

        // Look for parent of current
        const parentId: string = current.parent_span_id;
        const parentSpan = uniqueSpans.find(span => span.span_id === parentId);
        current = parentSpan ? spanMap[parentSpan.span_id] : undefined;
        depth++;
      }

      if (depth >= MAX_SPAN_DEPTH) {
        warnMalformedData('buildSpanTree', `Max depth exceeded for span ${s.span_id}`);
        roots.push(spanMap[s.span_id]);
        return;
      }

      spanMap[s.parent_span_id].children.push(spanMap[s.span_id]);
    } else {
      roots.push(spanMap[s.span_id]);
    }
  });

  return roots;
}

/**
 * Recursive component for rendering a span and its children in the tree
 */
function SpanNode({ span, level, maxDepth = MAX_SPAN_DEPTH }: SpanNodeProps) {
  // Prevent infinite recursion
  if (level > maxDepth) {
    return (
      <div className="text-red-400 text-xs ml-6 p-2 bg-red-500/10 rounded border border-red-500/30">
        Max depth exceeded
      </div>
    );
  }

  // Use fallback values for missing fields
  const serviceName = span.service_name || DEFAULTS.SERVICE;
  const operationName = span.operation_name || DEFAULTS.OPERATION;
  const status = sanitizeStatus(span.status);
  const attributes = sanitizeAttributes(span.attributes);

  return (
    <div className="border-l-2 border-slate-600/50 pl-4 ml-2">
      <div className="bg-slate-700/30 rounded-lg p-3 mb-2 border border-slate-600/30">
        <div className="flex justify-between items-start mb-1">
          <div>
            <span className="text-emerald-400 font-medium">{serviceName}</span>
            <span className="text-slate-500 mx-2">→</span>
            <span className="text-slate-300">{operationName}</span>
          </div>
          <div className="flex gap-2">
            <span className={`px-2 py-0.5 rounded text-xs ${status === 'OK' ? 'bg-emerald-500/20 text-emerald-400' : 'bg-red-500/20 text-red-400'}`}>
              {status}
            </span>
            <span className="text-slate-400 text-xs">{(span.duration_us / 1000).toFixed(2)}ms</span>
          </div>
        </div>
        <div className="text-xs text-slate-500 font-mono">{span.span_id}</div>
        {Object.keys(attributes).length > 0 && (
          <div className="mt-2 flex flex-wrap gap-1">
            {Object.entries(attributes).slice(0, 5).map(([k, v]) => (
              <span key={k} className="bg-slate-600/30 px-1.5 py-0.5 rounded text-xs text-slate-400">{k}={v}</span>
            ))}
          </div>
        )}
      </div>
      {span.children?.map(child => <SpanNode key={child.span_id} span={child} level={level + 1} maxDepth={maxDepth} />)}
    </div>
  );
}

/**
 * TraceDetailView displays the full details of a trace including metadata and span tree
 */
export function TraceDetailView({ trace, onClose }: TraceDetailViewProps) {
  // Guard against null/undefined trace.spans
  const spanTree = buildSpanTree(trace.spans ?? []);

  return (
    <div className="space-y-4">
      <div className="flex justify-between items-center">
        <div>
          <h2 className="text-xl font-semibold text-slate-100">Trace Detail</h2>
          <p className="text-slate-400 text-sm font-mono">{trace.trace_id}</p>
        </div>
        <button
          onClick={onClose}
          className="bg-amber-500 hover:bg-amber-600 text-slate-900 px-4 py-2 rounded-lg text-sm font-medium transition-colors"
        >
          ← Back
        </button>
      </div>

      <div className="grid grid-cols-4 gap-4">
        <MetricCard label="Service" value={trace.service_id?.split(':')[0] || '-'} />
        <MetricCard label="Duration" value={`${(trace.total_duration_us / 1000).toFixed(2)}ms`} />
        <MetricCard label="Spans" value={trace.spans?.length || 0} />
        <MetricCard label="Fingerprint" value={trace.fingerprint?.slice(0, 8) || '-'} />
      </div>

      <Card title="Timeline">
        <TraceWaterfall trace={trace} />
      </Card>

      <Card title="Span Tree">
        <div className="max-h-[500px] overflow-y-auto">
          {spanTree.map(root => <SpanNode key={root.span_id} span={root} level={0} />)}
        </div>
      </Card>
    </div>
  );
}
