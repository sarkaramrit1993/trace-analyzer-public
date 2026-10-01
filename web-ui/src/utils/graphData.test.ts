import { describe, it, expect } from 'vitest';
import { buildGraphData } from './graphData';
import type { TraceDetail } from '../types';

const span = (span_id: string, parent_span_id: string, service_name: string, operation_name: string) =>
  ({ span_id, parent_span_id, service_name, operation_name, duration_us: 100, status: 'OK' });

describe('buildGraphData', () => {
  it('returns an empty graph for a missing or empty trace', () => {
    expect(buildGraphData(undefined)).toEqual({ nodes: [], links: [] });
    expect(buildGraphData({ spans: [] } as unknown as TraceDetail)).toEqual({ nodes: [], links: [] });
  });

  it('aggregates spans into service:operation nodes and weighted parent->child links', () => {
    const trace = {
      spans: [
        span('root', '', 'gw', 'handle'),
        span('c1', 'root', 'db', 'query'),
        span('c2', 'root', 'db', 'query'),
        span('c3', 'c1', 'db', 'query'),
      ],
    } as unknown as TraceDetail;

    const { nodes, links } = buildGraphData(trace);

    expect(nodes.map(n => [n.id, n.count, n.isRoot])).toEqual([['gw:handle', 1, true], ['db:query', 3, false]]);
    expect(links).toEqual([{ source: 'gw:handle', target: 'db:query', value: 2, avgDuration: 100 }]);
  });
});
