import { useState } from 'react';

export interface GraphNode {
  id: string;
  service: string;
  operation: string;
  count: number;
  totalDuration: number;
  status: string;
  isRoot: boolean;
}

export interface GraphLink {
  source: string;
  target: string;
  value: number;
  avgDuration?: number;
}

interface LayoutNode extends GraphNode {
  x: number;
  y: number;
  size: number;
}

interface SimpleGraphProps {
  nodes: GraphNode[];
  links: GraphLink[];
  width?: number;
  height?: number;
}

export function SimpleGraph({ nodes, links, width = 600, height = 400 }: SimpleGraphProps) {
  const [hoveredNode, setHoveredNode] = useState<LayoutNode | null>(null);
  const [hoveredLink, setHoveredLink] = useState<number | null>(null);

  // Enhanced layout: hierarchical arrangement based on root nodes
  const center = { x: width / 2, y: height / 2 };
  const rootNodes = nodes.filter(n => n.isRoot);
  const nonRootNodes = nodes.filter(n => !n.isRoot);

  let layoutNodes: LayoutNode[] = [];

  if (rootNodes.length > 0) {
    // Place root nodes at the top
    rootNodes.forEach((node, i) => {
      layoutNodes.push({
        ...node,
        x: center.x + (i - (rootNodes.length - 1) / 2) * 100,
        y: height * 0.2,
        size: Math.max(20, Math.min(50, node.count * 6))
      });
    });

    // Place other nodes in concentric circles or hierarchical levels
    const radius = Math.min(width, height) / 4;
    nonRootNodes.forEach((node, i) => {
      const angle = (2 * Math.PI * i) / nonRootNodes.length;
      layoutNodes.push({
        ...node,
        x: center.x + Math.cos(angle) * radius,
        y: center.y + Math.sin(angle) * radius + 50,
        size: Math.max(15, Math.min(40, node.count * 5))
      });
    });
  } else {
    // Fallback to circular layout
    const radius = Math.min(width, height) / 3;
    layoutNodes = nodes.map((node, i) => {
      const angle = (2 * Math.PI * i) / nodes.length;
      return {
        ...node,
        x: center.x + Math.cos(angle) * radius,
        y: center.y + Math.sin(angle) * radius,
        size: Math.max(15, Math.min(40, node.count * 5))
      };
    });
  }

  return (
    <div className="relative">
      <svg width={width} height={height} className="border border-slate-700/30 rounded-lg bg-slate-900/50">
        <defs>
          {/* Enhanced arrow markers */}
          <marker
            id="arrowhead"
            markerWidth="12"
            markerHeight="8"
            refX="11"
            refY="4"
            orient="auto"
            markerUnits="strokeWidth"
          >
            <polygon
              points="0 0, 12 4, 0 8"
              fill="#10B981"
              stroke="#10B981"
              strokeWidth="1"
            />
          </marker>
          <marker
            id="arrowhead-hover"
            markerWidth="12"
            markerHeight="8"
            refX="11"
            refY="4"
            orient="auto"
            markerUnits="strokeWidth"
          >
            <polygon
              points="0 0, 12 4, 0 8"
              fill="#34D399"
              stroke="#34D399"
              strokeWidth="1"
            />
          </marker>
          <marker
            id="arrowhead-thick"
            markerWidth="14"
            markerHeight="10"
            refX="13"
            refY="5"
            orient="auto"
            markerUnits="strokeWidth"
          >
            <polygon
              points="0 0, 14 5, 0 10"
              fill="#F59E0B"
              stroke="#F59E0B"
              strokeWidth="1"
            />
          </marker>
        </defs>

        {/* Links with enhanced styling */}
        {links.map((link, i) => {
          const sourceNode = layoutNodes.find(n => n.id === link.source);
          const targetNode = layoutNodes.find(n => n.id === link.target);
          if (!sourceNode || !targetNode) return null;

          // Calculate link positioning to avoid overlapping nodes
          const dx = targetNode.x - sourceNode.x;
          const dy = targetNode.y - sourceNode.y;
          const length = Math.sqrt(dx * dx + dy * dy);
          const unitX = dx / length;
          const unitY = dy / length;

          const sourceOffset = sourceNode.size + 5;
          const targetOffset = targetNode.size + 15;

          const x1 = sourceNode.x + unitX * sourceOffset;
          const y1 = sourceNode.y + unitY * sourceOffset;
          const x2 = targetNode.x - unitX * targetOffset;
          const y2 = targetNode.y - unitY * targetOffset;

          const isHovered = hoveredLink === i;
          const isThick = link.value > 1;

          return (
            <g key={i}>
              <line
                x1={x1}
                y1={y1}
                x2={x2}
                y2={y2}
                stroke={isHovered ? "#34D399" : "#10B981"}
                strokeWidth={isThick ? 4 : 2}
                markerEnd={`url(#${isHovered ? 'arrowhead-hover' : isThick ? 'arrowhead-thick' : 'arrowhead'})`}
                className="cursor-pointer transition-all duration-200"
                onMouseEnter={() => setHoveredLink(i)}
                onMouseLeave={() => setHoveredLink(null)}
                opacity={isHovered ? 1 : 0.8}
              />
              {/* Flow direction indicator */}
              {isThick && (
                <text
                  x={(x1 + x2) / 2}
                  y={(y1 + y2) / 2 - 8}
                  textAnchor="middle"
                  className="fill-amber-400 text-xs font-bold"
                  pointerEvents="none"
                >
                  {link.value}x
                </text>
              )}
            </g>
          );
        })}

        {/* Nodes with enhanced styling */}
        {layoutNodes.map((node) => (
          <g key={node.id}>
            {/* Node circle with gradient effect */}
            <circle
              cx={node.x}
              cy={node.y}
              r={node.size}
              fill={node.status === 'OK' ? '#10B981' : '#EF4444'}
              stroke={node.isRoot ? "#F59E0B" : "#374151"}
              strokeWidth={node.isRoot ? 3 : 2}
              className="cursor-pointer transition-all duration-200"
              onMouseEnter={() => setHoveredNode(node)}
              onMouseLeave={() => setHoveredNode(null)}
              opacity={hoveredNode === node ? 1 : 0.9}
              transform={hoveredNode === node ? `scale(1.1)` : `scale(1)`}
              style={{ transformOrigin: `${node.x}px ${node.y}px` }}
            />

            {/* Root indicator */}
            {node.isRoot && (
              <text
                x={node.x}
                y={node.y - node.size - 8}
                textAnchor="middle"
                className="fill-amber-400 text-xs font-bold"
                pointerEvents="none"
              >
                ROOT
              </text>
            )}

            {/* Node labels */}
            <text
              x={node.x}
              y={node.y + node.size + 15}
              textAnchor="middle"
              className="fill-slate-100 text-xs font-medium"
              pointerEvents="none"
            >
              {node.operation.length > 12 ? node.operation.substring(0, 12) + '...' : node.operation}
            </text>
            <text
              x={node.x}
              y={node.y + node.size + 28}
              textAnchor="middle"
              className="fill-slate-400 text-xs"
              pointerEvents="none"
            >
              {node.service}
            </text>

            {/* Call count indicator */}
            {node.count > 1 && (
              <circle
                cx={node.x + node.size - 8}
                cy={node.y - node.size + 8}
                r="8"
                fill="#F59E0B"
                stroke="#0F172A"
                strokeWidth="1"
              />
            )}
            {node.count > 1 && (
              <text
                x={node.x + node.size - 8}
                y={node.y - node.size + 12}
                textAnchor="middle"
                className="fill-slate-900 text-xs font-bold"
                pointerEvents="none"
              >
                {node.count}
              </text>
            )}
          </g>
        ))}
      </svg>

      {/* Enhanced Tooltip */}
      {hoveredNode && (
        <div className="absolute top-4 left-4 bg-slate-800/90 backdrop-blur-sm text-slate-100 p-4 rounded-lg shadow-xl text-sm z-10 border border-slate-700">
          <div className="font-bold text-emerald-400 text-base">{hoveredNode.service}</div>
          <div className="text-slate-300 font-medium">{hoveredNode.operation}</div>
          <div className="mt-2 space-y-1">
            <div className="text-slate-400">Calls: <span className="text-emerald-400">{hoveredNode.count}</span></div>
            <div className="text-slate-400">Avg Duration: <span className="text-emerald-400">{(hoveredNode.totalDuration / hoveredNode.count / 1000).toFixed(2)}ms</span></div>
            <div className="text-slate-400">Status: <span className={hoveredNode.status === 'OK' ? 'text-emerald-400' : 'text-red-400'}>{hoveredNode.status}</span></div>
            {hoveredNode.isRoot && <div className="text-amber-400 font-medium">* Root Node</div>}
          </div>
        </div>
      )}

      {/* Link Tooltip */}
      {hoveredLink !== null && links[hoveredLink] && (
        <div className="absolute top-20 left-4 bg-slate-800/90 backdrop-blur-sm text-slate-100 p-3 rounded-lg shadow-xl text-sm z-10 border border-slate-700">
          <div className="font-medium text-emerald-400">Flow Direction</div>
          <div className="text-slate-300 text-xs mt-1">
            {links[hoveredLink].source.split(':')[1]} -&gt; {links[hoveredLink].target.split(':')[1]}
          </div>
          <div className="text-slate-400 text-xs">
            Calls: <span className="text-emerald-400">{links[hoveredLink].value}</span>
          </div>
        </div>
      )}
    </div>
  );
}

export default SimpleGraph;
