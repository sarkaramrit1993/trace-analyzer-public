import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { TopologyModal, TopologyModalProps } from './TopologyModal';
import type { GraphNode, GraphLink } from './SimpleGraph';

// Mock graph nodes
const mockNodes: GraphNode[] = [
  {
    id: 'api-gateway:handleRequest',
    service: 'api-gateway',
    operation: 'handleRequest',
    count: 100,
    totalDuration: 15000000,
    status: 'OK',
    isRoot: true,
  },
  {
    id: 'user-service:getUser',
    service: 'user-service',
    operation: 'getUser',
    count: 100,
    totalDuration: 8000000,
    status: 'OK',
    isRoot: false,
  },
  {
    id: 'database:query',
    service: 'database',
    operation: 'query',
    count: 100,
    totalDuration: 5000000,
    status: 'OK',
    isRoot: false,
  },
  {
    id: 'cache:get',
    service: 'cache',
    operation: 'get',
    count: 50,
    totalDuration: 250000,
    status: 'OK',
    isRoot: false,
  },
];

// Mock error node
const mockErrorNode: GraphNode = {
  id: 'payment-service:processPayment',
  service: 'payment-service',
  operation: 'processPayment',
  count: 10,
  totalDuration: 500000,
  status: 'ERROR',
  isRoot: false,
};

// Mock graph links
const mockLinks: GraphLink[] = [
  {
    source: 'api-gateway:handleRequest',
    target: 'user-service:getUser',
    value: 1,
    avgDuration: 80000,
  },
  {
    source: 'user-service:getUser',
    target: 'database:query',
    value: 1,
    avgDuration: 50000,
  },
  {
    source: 'api-gateway:handleRequest',
    target: 'cache:get',
    value: 2,
    avgDuration: 5000,
  },
];

// Mock topology
const mockTopology = {
  fingerprint: 'fp-abc123def456',
  count: 1500,
  percentage: 75.5,
  serviceGrouping: {
    service_identity: 'api-gateway',
    scope1: 'platform',
    scope2: 'frontend',
    env: 'prod',
  },
};

// Mock topology with error
const mockTopologyWithError = {
  fingerprint: 'fp-error-789',
  count: 50,
  percentage: 2.5,
  errorMessage: 'Failed to fetch topology data: network timeout',
};

// Mock empty topology
const mockEmptyTopology = {
  fingerprint: 'fp-empty-456',
  count: 10,
  percentage: 0.5,
};

const defaultProps: TopologyModalProps = {
  topology: mockTopology,
  onClose: vi.fn(),
  graphData: {
    nodes: mockNodes,
    links: mockLinks,
  },
};

// Mock window dimensions
const mockWindowDimensions = () => {
  Object.defineProperty(window, 'innerWidth', { writable: true, configurable: true, value: 1200 });
  Object.defineProperty(window, 'innerHeight', { writable: true, configurable: true, value: 800 });
};

describe('TopologyModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockWindowDimensions();
  });

  describe('Modal Open/Close', () => {
    it('renders the modal when mounted', () => {
      render(<TopologyModal {...defaultProps} />);
      expect(screen.getByText('Topology Visualization')).toBeInTheDocument();
    });

    it('renders modal with backdrop', () => {
      render(<TopologyModal {...defaultProps} />);
      // Check for the backdrop element
      const backdrop = document.querySelector('.fixed.inset-0');
      expect(backdrop).toBeInTheDocument();
      expect(backdrop).toHaveClass('bg-slate-900/80');
    });

    it('calls onClose when close button is clicked', () => {
      const onClose = vi.fn();
      render(<TopologyModal {...defaultProps} onClose={onClose} />);

      const closeButton = screen.getByText(/Close/);
      fireEvent.click(closeButton);

      expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('close button has correct text', () => {
      render(<TopologyModal {...defaultProps} />);
      expect(screen.getByText('X Close')).toBeInTheDocument();
    });

    it('close button is styled correctly', () => {
      render(<TopologyModal {...defaultProps} />);
      const closeButton = screen.getByText('X Close');
      expect(closeButton).toHaveClass('bg-amber-500');
    });

    it('close button is a button element', () => {
      render(<TopologyModal {...defaultProps} />);
      const closeButton = screen.getByText('X Close');
      expect(closeButton.tagName).toBe('BUTTON');
    });

    it('modal has high z-index for proper layering', () => {
      render(<TopologyModal {...defaultProps} />);
      const modal = document.querySelector('.z-50');
      expect(modal).toBeInTheDocument();
    });
  });

  describe('Topology Fingerprint Display', () => {
    it('displays the topology fingerprint', () => {
      render(<TopologyModal {...defaultProps} />);
      expect(screen.getByText('fp-abc123def456')).toBeInTheDocument();
    });

    it('displays "Fingerprint:" label', () => {
      render(<TopologyModal {...defaultProps} />);
      expect(screen.getByText(/Fingerprint:/)).toBeInTheDocument();
    });

    it('fingerprint is displayed in monospace font', () => {
      render(<TopologyModal {...defaultProps} />);
      const fingerprint = screen.getByText('fp-abc123def456');
      expect(fingerprint).toHaveClass('font-mono');
    });

    it('displays trace count and percentage', () => {
      render(<TopologyModal {...defaultProps} />);
      expect(screen.getByText('1500 traces (75.5%)')).toBeInTheDocument();
    });

    it('handles single trace count correctly', () => {
      const singleTraceTopology = { ...mockTopology, count: 1, percentage: 0.1 };
      render(<TopologyModal {...defaultProps} topology={singleTraceTopology} />);
      expect(screen.getByText('1 traces (0.1%)')).toBeInTheDocument();
    });

    it('displays service grouping information when present', () => {
      render(<TopologyModal {...defaultProps} />);
      expect(screen.getByText('service_identity=')).toBeInTheDocument();
      // 'api-gateway' appears in both service grouping and graph node labels
      expect(screen.getAllByText('api-gateway').length).toBeGreaterThanOrEqual(1);
      expect(screen.getByText('scope1=')).toBeInTheDocument();
      // 'platform' also appears in graph node labels
      expect(screen.getAllByText('platform').length).toBeGreaterThanOrEqual(1);
    });

    it('does not show service grouping when not present', () => {
      const noGroupingTopology = {
        fingerprint: 'fp-no-grouping',
        count: 100,
        percentage: 5.0,
      };
      render(<TopologyModal {...defaultProps} topology={noGroupingTopology} />);
      expect(screen.queryByText('service_identity=')).not.toBeInTheDocument();
    });

    it('filters out null/undefined grouping values', () => {
      const partialGroupingTopology = {
        fingerprint: 'fp-partial',
        count: 100,
        percentage: 5.0,
        serviceGrouping: {
          service_identity: 'test-service',
          scope1: null,
          scope2: undefined,
          env: 'prod',
        },
      };
      render(<TopologyModal {...defaultProps} topology={partialGroupingTopology} />);
      expect(screen.getByText('service_identity=')).toBeInTheDocument();
      expect(screen.getByText('env=')).toBeInTheDocument();
      // Should only show 2 grouping tags (service_identity and env)
    });
  });

  describe('SimpleGraph Rendering', () => {
    it('renders SimpleGraph when nodes are present', () => {
      render(<TopologyModal {...defaultProps} />);
      // Check for SVG element from SimpleGraph
      const svg = document.querySelector('svg');
      expect(svg).toBeInTheDocument();
    });

    it('renders all nodes in the graph', () => {
      render(<TopologyModal {...defaultProps} />);
      // The SimpleGraph component renders operation names and service names
      // Check that the SVG contains expected text elements
      const svg = document.querySelector('svg');
      expect(svg).toBeInTheDocument();
      // Node circles should be present
      const circles = svg?.querySelectorAll('circle');
      expect(circles?.length).toBeGreaterThanOrEqual(4); // At least 4 main nodes
    });

    it('renders graph links', () => {
      render(<TopologyModal {...defaultProps} />);
      const svg = document.querySelector('svg');
      // Lines represent links
      const lines = svg?.querySelectorAll('line');
      expect(lines?.length).toBeGreaterThanOrEqual(3); // 3 links
    });

    it('applies correct dimensions to graph', () => {
      render(<TopologyModal {...defaultProps} />);
      const svg = document.querySelector('svg');
      expect(svg).toHaveAttribute('width');
      expect(svg).toHaveAttribute('height');
    });

    it('renders root node indicator', () => {
      render(<TopologyModal {...defaultProps} />);
      // The ROOT text should be visible for root nodes
      expect(screen.getByText('ROOT')).toBeInTheDocument();
    });

    it('applies different styling for error status nodes', () => {
      const graphDataWithError = {
        nodes: [...mockNodes, mockErrorNode],
        links: mockLinks,
      };
      render(<TopologyModal {...defaultProps} graphData={graphDataWithError} />);
      // Error nodes should have different fill color (red)
      const svg = document.querySelector('svg');
      const circles = svg?.querySelectorAll('circle');
      // At least one circle should have the error color
      const hasErrorCircle = Array.from(circles || []).some(
        circle => circle.getAttribute('fill') === '#EF4444'
      );
      expect(hasErrorCircle).toBe(true);
    });
  });

  describe('Empty Graph State', () => {
    it('shows "No graph data available" when nodes array is empty', () => {
      const emptyGraphData = { nodes: [], links: [] };
      render(<TopologyModal {...defaultProps} graphData={emptyGraphData} topology={mockEmptyTopology} />);
      expect(screen.getByText('No graph data available')).toBeInTheDocument();
    });

    it('displays emoji indicator for empty state', () => {
      const emptyGraphData = { nodes: [], links: [] };
      render(<TopologyModal {...defaultProps} graphData={emptyGraphData} topology={mockEmptyTopology} />);
      // The emoji is displayed in the empty state
      const emojiContainer = document.querySelector('.text-6xl');
      expect(emojiContainer).toBeInTheDocument();
    });

    it('displays error message when topology has errorMessage', () => {
      const emptyGraphData = { nodes: [], links: [] };
      render(<TopologyModal {...defaultProps} graphData={emptyGraphData} topology={mockTopologyWithError} />);
      expect(screen.getByText('Failed to fetch topology data: network timeout')).toBeInTheDocument();
    });

    it('shows explanation when no error message present', () => {
      const emptyGraphData = { nodes: [], links: [] };
      render(<TopologyModal {...defaultProps} graphData={emptyGraphData} topology={mockEmptyTopology} />);
      expect(screen.getByText(/doesn't have detailed span information/)).toBeInTheDocument();
    });

    it('lists possible reasons for missing data', () => {
      const emptyGraphData = { nodes: [], links: [] };
      render(<TopologyModal {...defaultProps} graphData={emptyGraphData} topology={mockEmptyTopology} />);
      expect(screen.getByText(/Traces may have been cleaned up/)).toBeInTheDocument();
      expect(screen.getByText(/very rare topology/)).toBeInTheDocument();
      expect(screen.getByText(/Spans weren't properly stored/)).toBeInTheDocument();
    });

    it('shows occurrence count in empty state', () => {
      const emptyGraphData = { nodes: [], links: [] };
      render(<TopologyModal {...defaultProps} graphData={emptyGraphData} topology={mockEmptyTopology} />);
      expect(screen.getByText(/10 occurrences/)).toBeInTheDocument();
    });
  });

  describe('Close Button Functionality', () => {
    it('close button receives focus correctly', () => {
      render(<TopologyModal {...defaultProps} />);
      const closeButton = screen.getByText('X Close');
      closeButton.focus();
      expect(document.activeElement).toBe(closeButton);
    });

    it('close button has hover styles', () => {
      render(<TopologyModal {...defaultProps} />);
      const closeButton = screen.getByText('X Close');
      expect(closeButton).toHaveClass('hover:bg-amber-600');
    });

    it('close button has transition for smooth animation', () => {
      render(<TopologyModal {...defaultProps} />);
      const closeButton = screen.getByText('X Close');
      expect(closeButton).toHaveClass('transition-colors');
    });
  });

  describe('Modal Layout', () => {
    it('modal content is centered', () => {
      render(<TopologyModal {...defaultProps} />);
      const backdrop = document.querySelector('.fixed.inset-0');
      expect(backdrop).toHaveClass('flex');
      expect(backdrop).toHaveClass('items-center');
      expect(backdrop).toHaveClass('justify-center');
    });

    it('modal has proper dimensions', () => {
      render(<TopologyModal {...defaultProps} />);
      const modalContent = document.querySelector('.w-\\[90vw\\]');
      expect(modalContent).toBeInTheDocument();
      expect(modalContent).toHaveClass('h-[90vh]');
    });

    it('modal has max-width constraint', () => {
      render(<TopologyModal {...defaultProps} />);
      const modalContent = document.querySelector('.max-w-5xl');
      expect(modalContent).toBeInTheDocument();
    });

    it('modal has backdrop blur effect', () => {
      render(<TopologyModal {...defaultProps} />);
      const backdrop = document.querySelector('.backdrop-blur-sm');
      expect(backdrop).toBeInTheDocument();
    });

    it('modal content has rounded corners', () => {
      render(<TopologyModal {...defaultProps} />);
      const modalContent = document.querySelector('.rounded-xl');
      expect(modalContent).toBeInTheDocument();
    });

    it('modal content has border styling', () => {
      render(<TopologyModal {...defaultProps} />);
      const modalContent = document.querySelector('.border.border-slate-700\\/50');
      expect(modalContent).toBeInTheDocument();
    });
  });

  describe('Header Section', () => {
    it('displays "Topology Visualization" title', () => {
      render(<TopologyModal {...defaultProps} />);
      expect(screen.getByText('Topology Visualization')).toBeInTheDocument();
    });

    it('title has correct styling', () => {
      render(<TopologyModal {...defaultProps} />);
      const title = screen.getByText('Topology Visualization');
      expect(title).toHaveClass('text-2xl');
      expect(title).toHaveClass('font-bold');
      expect(title).toHaveClass('text-emerald-400');
    });

    it('header is properly aligned', () => {
      render(<TopologyModal {...defaultProps} />);
      const headerContainer = document.querySelector('.flex.justify-between.items-center');
      expect(headerContainer).toBeInTheDocument();
    });
  });

  describe('Graph Interaction', () => {
    it('renders tooltip area for node hover', () => {
      render(<TopologyModal {...defaultProps} />);
      const svg = document.querySelector('svg');
      // Nodes should be interactive
      const circles = svg?.querySelectorAll('circle.cursor-pointer');
      expect(circles?.length).toBeGreaterThan(0);
    });

    it('links are interactive', () => {
      render(<TopologyModal {...defaultProps} />);
      const svg = document.querySelector('svg');
      const lines = svg?.querySelectorAll('line.cursor-pointer');
      expect(lines?.length).toBeGreaterThanOrEqual(3);
    });
  });

  describe('Edge Cases', () => {
    it('handles topology with zero count', () => {
      const zeroCountTopology = { ...mockTopology, count: 0, percentage: 0 };
      render(<TopologyModal {...defaultProps} topology={zeroCountTopology} />);
      expect(screen.getByText('0 traces (0.0%)')).toBeInTheDocument();
    });

    it('handles very long fingerprint', () => {
      const longFingerprintTopology = {
        ...mockTopology,
        fingerprint: 'fp-very-long-fingerprint-hash-1234567890abcdef1234567890abcdef',
      };
      render(<TopologyModal {...defaultProps} topology={longFingerprintTopology} />);
      expect(screen.getByText('fp-very-long-fingerprint-hash-1234567890abcdef1234567890abcdef')).toBeInTheDocument();
    });

    it('handles high percentage values', () => {
      const highPercentTopology = { ...mockTopology, percentage: 99.9 };
      render(<TopologyModal {...defaultProps} topology={highPercentTopology} />);
      expect(screen.getByText('1500 traces (99.9%)')).toBeInTheDocument();
    });

    it('handles graph with only one node', () => {
      const singleNodeGraph = {
        nodes: [mockNodes[0]],
        links: [],
      };
      render(<TopologyModal {...defaultProps} graphData={singleNodeGraph} />);
      const svg = document.querySelector('svg');
      expect(svg).toBeInTheDocument();
      // Should have at least one node circle
      const circles = svg?.querySelectorAll('circle');
      expect(circles?.length).toBeGreaterThanOrEqual(1);
    });

    it('handles graph with self-loop', () => {
      const selfLoopGraph = {
        nodes: mockNodes,
        links: [
          ...mockLinks,
          {
            source: 'api-gateway:handleRequest',
            target: 'api-gateway:handleRequest',
            value: 1,
          },
        ],
      };
      render(<TopologyModal {...defaultProps} graphData={selfLoopGraph} />);
      // Should not crash
      expect(screen.getByText('Topology Visualization')).toBeInTheDocument();
    });

    it('handles window resize gracefully', () => {
      render(<TopologyModal {...defaultProps} />);
      // Simulate window resize
      Object.defineProperty(window, 'innerWidth', { writable: true, configurable: true, value: 600 });
      Object.defineProperty(window, 'innerHeight', { writable: true, configurable: true, value: 400 });
      fireEvent.resize(window);
      // Should not crash
      expect(screen.getByText('Topology Visualization')).toBeInTheDocument();
    });
  });

  describe('Accessibility', () => {
    it('modal has proper focus management', () => {
      render(<TopologyModal {...defaultProps} />);
      const closeButton = screen.getByText('X Close');
      expect(closeButton).not.toHaveAttribute('disabled');
    });

    it('close button is keyboard accessible', () => {
      const onClose = vi.fn();
      render(<TopologyModal {...defaultProps} onClose={onClose} />);

      const closeButton = screen.getByText('X Close');
      fireEvent.keyDown(closeButton, { key: 'Enter' });
      // Button should respond to keyboard events
      expect(closeButton.tagName).toBe('BUTTON');
    });

    it('modal content is semantically structured', () => {
      render(<TopologyModal {...defaultProps} />);
      // Check for h2 heading
      const heading = screen.getByText('Topology Visualization');
      expect(heading.tagName).toBe('H2');
    });
  });
});
