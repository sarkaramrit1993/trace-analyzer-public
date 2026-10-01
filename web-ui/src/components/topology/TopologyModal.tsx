// React import needed for JSX transform in some configurations
import { SimpleGraph, GraphNode, GraphLink } from './SimpleGraph';

// Generic service grouping that accepts any key-value pairs
type ServiceGroupingLike = Record<string, string | null | undefined> | null | undefined;

/**
 * Topology data structure for the modal display
 */
export interface TopologyForModal {
  fingerprint: string;
  count: number;
  percentage: number;
  serviceGrouping?: ServiceGroupingLike;
  errorMessage?: string | null;
}

interface GraphData {
  nodes: GraphNode[];
  links: GraphLink[];
}

export interface TopologyModalProps {
  topology: TopologyForModal;
  onClose: () => void;
  graphData: GraphData;
}

export function TopologyModal({ topology, onClose, graphData }: TopologyModalProps) {
  return (
    <div className="fixed inset-0 bg-slate-900/80 backdrop-blur-sm z-50 flex items-center justify-center">
      <div className="bg-slate-800/90 backdrop-blur-sm rounded-xl border border-slate-700/50 w-[90vw] h-[90vh] max-w-5xl p-6">
        <div className="flex justify-between items-center mb-6">
          <div>
            <h2 className="text-2xl font-bold text-emerald-400">Topology Visualization</h2>
            <p className="text-slate-400 text-sm">
              Fingerprint: <span className="font-mono text-emerald-300">{topology.fingerprint}</span>
            </p>
            <p className="text-slate-400 text-sm">
              {topology.count} traces ({topology.percentage.toFixed(1)}%)
            </p>
            {topology.serviceGrouping && (
              <div className="mt-2 flex flex-wrap gap-2 text-xs text-slate-400">
                {Object.entries(topology.serviceGrouping)
                  .filter(([, v]) => v)
                  .map(([k, v]) => (
                    <span key={k} className="bg-slate-900/60 border border-slate-700/50 rounded px-2 py-0.5">
                      <span className="text-slate-500">{k}=</span>{v}
                    </span>
                  ))}
              </div>
            )}
          </div>
          <button
            onClick={onClose}
            className="bg-amber-500 hover:bg-amber-600 text-slate-900 px-4 py-2 rounded-lg text-sm font-medium transition-colors"
          >
            X Close
          </button>
        </div>

        <div className="flex flex-col items-center justify-center h-[calc(90vh-200px)]">
          {graphData.nodes.length > 0 ? (
            <SimpleGraph
              nodes={graphData.nodes}
              links={graphData.links}
              width={Math.min(800, typeof window !== 'undefined' ? window.innerWidth - 200 : 600)}
              height={Math.min(600, typeof window !== 'undefined' ? window.innerHeight - 300 : 400)}
            />
          ) : (
            <div className="text-slate-400 text-center max-w-2xl px-6">
              <div className="text-6xl mb-4">📊</div>
              <div className="text-xl font-semibold text-slate-300 mb-3">No graph data available</div>
              {topology.errorMessage ? (
                <div className="text-sm text-slate-400 leading-relaxed bg-slate-800/50 rounded-lg p-4 border border-slate-700/50">
                  {topology.errorMessage}
                </div>
              ) : (
                <div className="text-sm text-slate-500 space-y-2">
                  <p>This topology doesn't have detailed span information available.</p>
                  <p className="text-xs mt-3">Possible reasons:</p>
                  <ul className="text-xs text-left mt-2 space-y-1 list-disc list-inside text-slate-500">
                    <li>Traces may have been cleaned up (older than retention period)</li>
                    <li>This is a very rare topology with no stored examples</li>
                    <li>Spans weren't properly stored for this trace</li>
                  </ul>
                  {topology.count && (
                    <p className="text-xs mt-3 text-slate-400">
                      This topology has {topology.count} occurrence{topology.count !== 1 ? 's' : ''} but no trace details are stored.
                    </p>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

export default TopologyModal;
