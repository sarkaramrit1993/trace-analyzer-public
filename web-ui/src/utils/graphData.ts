import type { TraceDetail } from '../types';
import type { GraphNode, GraphLink } from '../components/topology/SimpleGraph';

export function buildGraphData(trace: TraceDetail | undefined): { nodes: GraphNode[]; links: GraphLink[] } {
  if (!trace || !trace.spans || !Array.isArray(trace.spans) || trace.spans.length === 0) {
    return { nodes: [], links: [] };
  }

  const nodes: GraphNode[] = [];
  const links: GraphLink[] = [];
  const nodeMap = new Map<string, GraphNode>();
  const spanMap = new Map<string, typeof trace.spans[0]>();

  // Build span lookup for parent-child relationships
  trace.spans.forEach(span => {
    if (span && span.span_id) {
      spanMap.set(span.span_id, span);
    }
  });

  // Create nodes with better aggregation
  trace.spans.forEach(span => {
    if (!span || !span.service_name || !span.operation_name) {
      return; // Skip invalid spans
    }
    const nodeId = `${span.service_name}:${span.operation_name}`;
    if (!nodeMap.has(nodeId)) {
      nodeMap.set(nodeId, {
        id: nodeId,
        service: span.service_name,
        operation: span.operation_name,
        count: 0,
        totalDuration: 0,
        status: span.status || 'OK',
        isRoot: !span.parent_span_id
      });
    }
    const node = nodeMap.get(nodeId)!;
    node.count++;
    node.totalDuration += (span.duration_us || 0);
    // Keep OK status if any span is OK
    if (span.status === 'OK') node.status = 'OK';
  });

  nodes.push(...Array.from(nodeMap.values()));

  // Create directional links based on parent->child relationships
  trace.spans.forEach(span => {
    if (!span || !span.span_id) return; // Skip invalid spans
    if (span.parent_span_id) {
      const parentSpan = spanMap.get(span.parent_span_id);
      if (parentSpan) {
        const parentNodeId = `${parentSpan.service_name}:${parentSpan.operation_name}`;
        const childNodeId = `${span.service_name}:${span.operation_name}`;

        // Only create link if nodes are different (avoid self-loops)
        if (parentNodeId !== childNodeId) {
          const existingLink = links.find(l => l.source === parentNodeId && l.target === childNodeId);
          if (existingLink) {
            existingLink.value += 1; // Increment weight for multiple calls
          } else {
            links.push({
              source: parentNodeId,
              target: childNodeId,
              value: 1,
              avgDuration: span.duration_us
            });
          }
        }
      }
    }
  });

  return { nodes, links };
}
