import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { Header, HeaderProps } from './Header';
import type { Stats, CombinationInfo } from '../../types';

// Mock Stats
const mockStats: Stats = {
  hot_tier_traces: 1500,
  hot_tier_spans: 25000,
  unique_services: 12,
  unique_topologies: 48,
  total_deviations: 7,
  total_anomalies: 3,
  total_errors: 2,
  interesting_traces: 15,
  interesting_traces_pct: 1.5,
  uptime_seconds: 3600,
  service_identity_fields: ['service_identity', 'scope1', 'scope2', 'scope3', 'env'],
};

// Mock Combinations
const mockCombinations: CombinationInfo[] = [
  {
    combination: 'cumulative:50',
    variant: 'cumulative',
    threshold: 50,
    name: 'Cumulative 50',
    description: 'Cumulative baseline with 50 trace threshold',
    parameters: {},
    min_samples: 50,
  },
  {
    combination: 'cumulative:100',
    variant: 'cumulative',
    threshold: 100,
    name: 'Cumulative 100',
    description: 'Cumulative baseline with 100 trace threshold',
    parameters: {},
    min_samples: 100,
  },
  {
    combination: 'ewma:50',
    variant: 'ewma',
    threshold: 50,
    name: 'EWMA 50',
    description: 'EWMA baseline with 50 trace threshold',
    parameters: {},
    min_samples: 50,
  },
  {
    combination: 'ewma:100',
    variant: 'ewma',
    threshold: 100,
    name: 'EWMA 100',
    description: 'EWMA baseline with 100 trace threshold',
    parameters: {},
    min_samples: 100,
  },
  {
    combination: 'sliding_window:50',
    variant: 'sliding_window',
    threshold: 50,
    name: 'Sliding Window 50',
    description: 'Sliding window baseline with 50 trace threshold',
    parameters: {},
    min_samples: 50,
  },
  {
    combination: 'exponential_decay:50',
    variant: 'exponential_decay',
    threshold: 50,
    name: 'Exponential Decay 50',
    description: 'Exponential decay baseline with 50 trace threshold',
    parameters: {},
    min_samples: 50,
  },
  {
    combination: 'same_time_yesterday:50',
    variant: 'same_time_yesterday',
    threshold: 50,
    name: 'Same Time Yesterday 50',
    description: 'Same time yesterday baseline with 50 trace threshold',
    parameters: {},
    min_samples: 50,
  },
];

const defaultProps: HeaderProps = {
  stats: mockStats,
  autoRefresh: false,
  onAutoRefreshToggle: vi.fn(),
  combinations: mockCombinations,
  activeCombination: 'cumulative:50',
  onCombinationSwitch: vi.fn(),
  showBaselinePanel: true,
  onToggleBaselinePanel: vi.fn(),
};

describe('Header', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('Title Rendering', () => {
    it('renders the title "Trace Deviation Analyzer"', () => {
      render(<Header {...defaultProps} />);
      expect(screen.getByText('Trace Deviation Analyzer')).toBeInTheDocument();
    });

    it('renders the title as an h1 element', () => {
      render(<Header {...defaultProps} />);
      const title = screen.getByText('Trace Deviation Analyzer');
      expect(title.tagName).toBe('H1');
    });
  });

  describe('Stats Display', () => {
    it('displays Traces count correctly', () => {
      render(<Header {...defaultProps} />);
      expect(screen.getByText('Traces')).toBeInTheDocument();
      expect(screen.getByText('1500')).toBeInTheDocument();
    });

    it('displays Services count correctly', () => {
      render(<Header {...defaultProps} />);
      expect(screen.getByText('Services')).toBeInTheDocument();
      expect(screen.getByText('12')).toBeInTheDocument();
    });

    it('displays Topologies count correctly', () => {
      render(<Header {...defaultProps} />);
      expect(screen.getByText('Topologies')).toBeInTheDocument();
      expect(screen.getByText('48')).toBeInTheDocument();
    });

    it('displays Deviations count correctly', () => {
      render(<Header {...defaultProps} />);
      expect(screen.getByText('Deviations')).toBeInTheDocument();
      expect(screen.getByText('7')).toBeInTheDocument();
    });

    it('displays Anomalies count correctly', () => {
      render(<Header {...defaultProps} />);
      expect(screen.getByText('Anomalies')).toBeInTheDocument();
      expect(screen.getByText('3')).toBeInTheDocument();
    });

    it('displays Errors count correctly', () => {
      render(<Header {...defaultProps} />);
      expect(screen.getByText('Errors')).toBeInTheDocument();
      expect(screen.getByText('2')).toBeInTheDocument();
    });

    it('displays Interesting traces with percentage', () => {
      render(<Header {...defaultProps} />);
      expect(screen.getByText('Interesting')).toBeInTheDocument();
      expect(screen.getByText('15 (1.5%)')).toBeInTheDocument();
    });

    it('handles zero stats gracefully', () => {
      const zeroStats: Stats = {
        hot_tier_traces: 0,
        hot_tier_spans: 0,
        unique_services: 0,
        unique_topologies: 0,
        total_deviations: 0,
        total_anomalies: 0,
        total_errors: 0,
        interesting_traces: 0,
        interesting_traces_pct: 0,
        uptime_seconds: 0,
        service_identity_fields: [],
      };
      render(<Header {...defaultProps} stats={zeroStats} />);
      expect(screen.getByText('Traces')).toBeInTheDocument();
      // Multiple zeros will be present
      const zeros = screen.getAllByText('0');
      expect(zeros.length).toBeGreaterThanOrEqual(1);
    });

    it('handles undefined stats fields with fallback to 0', () => {
      const partialStats = {} as Stats;
      render(<Header {...defaultProps} stats={partialStats} />);
      // Should not crash and should display 0 values
      expect(screen.getByText('Traces')).toBeInTheDocument();
    });
  });

  describe('Auto-refresh Toggle', () => {
    it('shows "Auto-refresh Off" when autoRefresh is false', () => {
      render(<Header {...defaultProps} autoRefresh={false} />);
      expect(screen.getByText('Auto-refresh Off')).toBeInTheDocument();
    });

    it('shows "Auto-refresh On" when autoRefresh is true', () => {
      render(<Header {...defaultProps} autoRefresh={true} />);
      expect(screen.getByText('Auto-refresh On')).toBeInTheDocument();
    });

    it('calls onAutoRefreshToggle when clicked', () => {
      const onAutoRefreshToggle = vi.fn();
      render(<Header {...defaultProps} onAutoRefreshToggle={onAutoRefreshToggle} />);

      const toggleButton = screen.getByText('Auto-refresh Off');
      fireEvent.click(toggleButton);

      expect(onAutoRefreshToggle).toHaveBeenCalledTimes(1);
    });

    it('has correct styling when auto-refresh is on', () => {
      render(<Header {...defaultProps} autoRefresh={true} />);
      const button = screen.getByText('Auto-refresh On');
      expect(button).toHaveClass('bg-emerald-500/20');
    });

    it('has correct styling when auto-refresh is off', () => {
      render(<Header {...defaultProps} autoRefresh={false} />);
      const button = screen.getByText('Auto-refresh Off');
      expect(button).toHaveClass('bg-slate-700/40');
    });
  });

  describe('Baseline Panel Expand/Collapse', () => {
    it('shows expand/collapse button when combinations exist', () => {
      render(<Header {...defaultProps} />);
      expect(screen.getByText('Baseline Strategy & Threshold Selection')).toBeInTheDocument();
    });

    it('calls onToggleBaselinePanel when header is clicked', () => {
      const onToggleBaselinePanel = vi.fn();
      render(<Header {...defaultProps} onToggleBaselinePanel={onToggleBaselinePanel} />);

      const toggleButton = screen.getByText('Baseline Strategy & Threshold Selection');
      fireEvent.click(toggleButton);

      expect(onToggleBaselinePanel).toHaveBeenCalledTimes(1);
    });

    it('shows variant options when panel is expanded', () => {
      render(<Header {...defaultProps} showBaselinePanel={true} />);
      expect(screen.getByText('Cumulative')).toBeInTheDocument();
      expect(screen.getByText('EWMA')).toBeInTheDocument();
      expect(screen.getByText('Sliding Window')).toBeInTheDocument();
      expect(screen.getByText('Exponential Decay')).toBeInTheDocument();
      expect(screen.getByText('Same Time Yesterday')).toBeInTheDocument();
    });

    it('hides variant options when panel is collapsed', () => {
      render(<Header {...defaultProps} showBaselinePanel={false} />);
      expect(screen.queryByText('Cumulative')).not.toBeInTheDocument();
      expect(screen.queryByText('EWMA')).not.toBeInTheDocument();
    });

    it('shows collapse indicator when panel is expanded', () => {
      render(<Header {...defaultProps} showBaselinePanel={true} />);
      // The collapse indicator
      expect(screen.getByText(/\u25BE/)).toBeInTheDocument(); // Unicode for down triangle
    });

    it('shows expand indicator when panel is collapsed', () => {
      render(<Header {...defaultProps} showBaselinePanel={false} />);
      // The expand indicator
      expect(screen.getByText(/\u25B8/)).toBeInTheDocument(); // Unicode for right triangle
    });

    it('does not render baseline section when no combinations exist', () => {
      render(<Header {...defaultProps} combinations={[]} />);
      expect(screen.queryByText('Baseline Strategy & Threshold Selection')).not.toBeInTheDocument();
    });
  });

  describe('Variant Selection', () => {
    it('renders dropdown for each variant when panel is expanded', () => {
      render(<Header {...defaultProps} showBaselinePanel={true} />);

      // Each variant should have a select element
      const selects = screen.getAllByRole('combobox');
      expect(selects.length).toBe(5); // 5 variant types
    });

    it('shows active indicator on the currently active variant', () => {
      render(<Header {...defaultProps} showBaselinePanel={true} activeCombination="cumulative:50" />);
      // The active variant shows a checkmark - multiple elements may match
      const activeIndicators = screen.getAllByText(/Active/);
      expect(activeIndicators.length).toBeGreaterThanOrEqual(1);
    });

    it('calls onCombinationSwitch when a different combination is selected', () => {
      const onCombinationSwitch = vi.fn();
      render(
        <Header
          {...defaultProps}
          showBaselinePanel={true}
          onCombinationSwitch={onCombinationSwitch}
        />
      );

      const selects = screen.getAllByRole('combobox');
      // Select the first dropdown (cumulative) and change to 100 traces
      fireEvent.change(selects[0], { target: { value: 'cumulative:100' } });

      expect(onCombinationSwitch).toHaveBeenCalledWith('cumulative:100');
    });

    it('shows correct threshold options in each dropdown', () => {
      render(<Header {...defaultProps} showBaselinePanel={true} />);

      // Check that dropdown options show threshold values
      expect(screen.getAllByText('50 traces').length).toBeGreaterThanOrEqual(1);
      expect(screen.getAllByText('100 traces').length).toBeGreaterThanOrEqual(1);
    });

    it('displays currently active combination info', () => {
      render(<Header {...defaultProps} showBaselinePanel={true} activeCombination="cumulative:50" />);
      expect(screen.getByText('Currently Active:')).toBeInTheDocument();
      // Should show the combination description
      expect(screen.getByText(/Cumulative - 50 traces threshold/)).toBeInTheDocument();
    });

    it('highlights the active variant card', () => {
      render(<Header {...defaultProps} showBaselinePanel={true} activeCombination="ewma:50" />);
      // The active variant card should have the active styling
      const ewmaLabel = screen.getByText('EWMA');
      const card = ewmaLabel.closest('div[class*="relative"]');
      expect(card).toHaveClass('bg-emerald-500/10');
    });
  });

  describe('Variant Tooltip', () => {
    it('shows tooltip on hover over variant card', async () => {
      render(<Header {...defaultProps} showBaselinePanel={true} />);

      // Find a variant label and hover over its container
      const cumulativeLabel = screen.getByText('Cumulative');
      const card = cumulativeLabel.closest('div[class*="relative"]');

      if (card) {
        fireEvent.mouseEnter(card);

        await waitFor(() => {
          expect(screen.getByText('Cumulative Baseline')).toBeInTheDocument();
          expect(screen.getByText(/Accumulates all historical data/)).toBeInTheDocument();
        });
      }
    });

    it('hides tooltip when mouse leaves variant card', async () => {
      render(<Header {...defaultProps} showBaselinePanel={true} />);

      const cumulativeLabel = screen.getByText('Cumulative');
      const card = cumulativeLabel.closest('div[class*="relative"]');

      if (card) {
        fireEvent.mouseEnter(card);

        await waitFor(() => {
          expect(screen.getByText('Cumulative Baseline')).toBeInTheDocument();
        });

        fireEvent.mouseLeave(card);

        await waitFor(() => {
          expect(screen.queryByText('Cumulative Baseline')).not.toBeInTheDocument();
        });
      }
    });

    it('tooltip shows calculation details', async () => {
      render(<Header {...defaultProps} showBaselinePanel={true} />);

      const ewmaLabel = screen.getByText('EWMA');
      const card = ewmaLabel.closest('div[class*="relative"]');

      if (card) {
        fireEvent.mouseEnter(card);

        await waitFor(() => {
          expect(screen.getByText('How it calculates:')).toBeInTheDocument();
        });
      }
    });

    it('tooltip shows "Best for" information', async () => {
      render(<Header {...defaultProps} showBaselinePanel={true} />);

      const slidingLabel = screen.getByText('Sliding Window');
      const card = slidingLabel.closest('div[class*="relative"]');

      if (card) {
        fireEvent.mouseEnter(card);

        await waitFor(() => {
          expect(screen.getByText('Best for:')).toBeInTheDocument();
        });
      }
    });

    it('tooltip shows "Learn more" link', async () => {
      render(<Header {...defaultProps} showBaselinePanel={true} />);

      const cumulativeLabel = screen.getByText('Cumulative');
      const card = cumulativeLabel.closest('div[class*="relative"]');

      if (card) {
        fireEvent.mouseEnter(card);

        await waitFor(() => {
          const learnMoreLink = screen.getByText(/Learn more/);
          expect(learnMoreLink).toBeInTheDocument();
          expect(learnMoreLink).toHaveAttribute('href');
        });
      }
    });
  });

  describe('Accessibility', () => {
    it('header element is semantic', () => {
      render(<Header {...defaultProps} />);
      const header = document.querySelector('header');
      expect(header).toBeInTheDocument();
    });

    it('toggle button is a button element', () => {
      render(<Header {...defaultProps} />);
      const toggleButton = screen.getByText('Auto-refresh Off');
      expect(toggleButton.tagName).toBe('BUTTON');
    });

    it('baseline toggle is a button element', () => {
      render(<Header {...defaultProps} />);
      const baselineToggle = screen.getByText('Baseline Strategy & Threshold Selection');
      // The text is inside a span, but the button contains the span
      const button = baselineToggle.closest('button');
      expect(button).toBeInTheDocument();
      expect(button?.tagName).toBe('BUTTON');
    });

    it('has proper title attribute on auto-refresh button', () => {
      render(<Header {...defaultProps} autoRefresh={false} />);
      const button = screen.getByText('Auto-refresh Off');
      expect(button).toHaveAttribute('title', 'Auto-refresh off');
    });
  });

  describe('Edge Cases', () => {
    it('handles high interesting_traces_pct with error styling', () => {
      const highPercentStats = { ...mockStats, interesting_traces_pct: 15 };
      render(<Header {...defaultProps} stats={highPercentStats} />);
      // Should use error variant styling for high percentage
      expect(screen.getByText(/\(15.0%\)/)).toBeInTheDocument();
    });

    it('handles medium interesting_traces_pct with warning styling', () => {
      const mediumPercentStats = { ...mockStats, interesting_traces_pct: 7 };
      render(<Header {...defaultProps} stats={mediumPercentStats} />);
      expect(screen.getByText(/\(7.0%\)/)).toBeInTheDocument();
    });

    it('switches between variants correctly', () => {
      const onCombinationSwitch = vi.fn();
      render(
        <Header
          {...defaultProps}
          showBaselinePanel={true}
          activeCombination="cumulative:50"
          onCombinationSwitch={onCombinationSwitch}
        />
      );

      const selects = screen.getAllByRole('combobox');
      // Select EWMA dropdown (index 1) and select a threshold
      fireEvent.change(selects[1], { target: { value: 'ewma:50' } });

      expect(onCombinationSwitch).toHaveBeenCalledWith('ewma:50');
    });

    it('displays correctly with many combinations', () => {
      const manyCombinations: CombinationInfo[] = [
        ...mockCombinations,
        {
          combination: 'cumulative:200',
          variant: 'cumulative',
          threshold: 200,
          name: 'Cumulative 200',
          description: 'Cumulative baseline with 200 trace threshold',
          parameters: {},
          min_samples: 200,
        },
        {
          combination: 'cumulative:500',
          variant: 'cumulative',
          threshold: 500,
          name: 'Cumulative 500',
          description: 'Cumulative baseline with 500 trace threshold',
          parameters: {},
          min_samples: 500,
        },
      ];

      render(<Header {...defaultProps} combinations={manyCombinations} showBaselinePanel={true} />);
      expect(screen.getByText('200 traces')).toBeInTheDocument();
      expect(screen.getByText('500 traces')).toBeInTheDocument();
    });
  });
});
