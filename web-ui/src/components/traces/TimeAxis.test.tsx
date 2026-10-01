import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { TimeAxis, TimeAxisCompact } from './TimeAxis';

describe('TimeAxis', () => {
  describe('Basic Rendering', () => {
    it('renders time markers with default 5 ticks', () => {
      render(<TimeAxis durationUs={100000} />); // 100ms total

      // Should render 5 tick marks: 0ms, 25ms, 50ms, 75ms, 100ms
      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('25.00ms')).toBeInTheDocument();
      expect(screen.getByText('50.00ms')).toBeInTheDocument();
      expect(screen.getByText('75.00ms')).toBeInTheDocument();
      expect(screen.getByText('100.00ms')).toBeInTheDocument();
    });

    it('has presentation role for accessibility', () => {
      const { container } = render(<TimeAxis durationUs={100000} />);
      const axis = container.querySelector('[role="presentation"]');
      expect(axis).toBeInTheDocument();
    });

    it('is hidden from screen readers', () => {
      const { container } = render(<TimeAxis durationUs={100000} />);
      const axis = container.querySelector('[aria-hidden="true"]');
      expect(axis).toBeInTheDocument();
    });
  });

  describe('Configurable Number of Ticks', () => {
    it('renders 3 ticks when specified', () => {
      render(<TimeAxis durationUs={100000} ticks={3} />); // 100ms total

      // Should render 3 tick marks: 0ms, 50ms, 100ms
      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('50.00ms')).toBeInTheDocument();
      expect(screen.getByText('100.00ms')).toBeInTheDocument();

      // Should not render intermediate ticks
      expect(screen.queryByText('25.00ms')).not.toBeInTheDocument();
      expect(screen.queryByText('75.00ms')).not.toBeInTheDocument();
    });

    it('renders 7 ticks when specified', () => {
      render(<TimeAxis durationUs={120000} ticks={7} />); // 120ms total

      // Should render 7 tick marks at 20ms intervals
      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('20.00ms')).toBeInTheDocument();
      expect(screen.getByText('40.00ms')).toBeInTheDocument();
      expect(screen.getByText('60.00ms')).toBeInTheDocument();
      expect(screen.getByText('80.00ms')).toBeInTheDocument();
      expect(screen.getByText('100.00ms')).toBeInTheDocument();
      expect(screen.getByText('120.00ms')).toBeInTheDocument();
    });

    it('handles 2 ticks (minimum meaningful)', () => {
      render(<TimeAxis durationUs={100000} ticks={2} />); // 100ms total

      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('100.00ms')).toBeInTheDocument();
    });

    it('handles 1 tick by showing only 0ms', () => {
      render(<TimeAxis durationUs={100000} ticks={1} />);

      expect(screen.getByText('0ms')).toBeInTheDocument();
    });
  });

  describe('Time Labels Based on Duration', () => {
    it('shows correct labels for short duration (milliseconds)', () => {
      render(<TimeAxis durationUs={4000} ticks={5} />); // 4ms total

      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('1.00ms')).toBeInTheDocument();
      expect(screen.getByText('2.00ms')).toBeInTheDocument();
      expect(screen.getByText('3.00ms')).toBeInTheDocument();
      expect(screen.getByText('4.00ms')).toBeInTheDocument();
    });

    it('shows correct labels for medium duration', () => {
      render(<TimeAxis durationUs={200000} ticks={5} />); // 200ms total

      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('50.00ms')).toBeInTheDocument();
      expect(screen.getByText('100.00ms')).toBeInTheDocument();
      expect(screen.getByText('150.00ms')).toBeInTheDocument();
      expect(screen.getByText('200.00ms')).toBeInTheDocument();
    });

    it('shows correct labels for long duration (seconds)', () => {
      render(<TimeAxis durationUs={4000000} ticks={5} />); // 4s total

      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('1.00s')).toBeInTheDocument();
      expect(screen.getByText('2.00s')).toBeInTheDocument();
      expect(screen.getByText('3.00s')).toBeInTheDocument();
      expect(screen.getByText('4.00s')).toBeInTheDocument();
    });

    it('shows correct labels for very long duration (minutes)', () => {
      render(<TimeAxis durationUs={120000000} ticks={3} />); // 120s = 2 minutes

      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('1.00m')).toBeInTheDocument();
      expect(screen.getByText('2.00m')).toBeInTheDocument();
    });
  });

  describe('Time Formatting (ms vs s)', () => {
    it('formats sub-second durations in milliseconds', () => {
      render(<TimeAxis durationUs={500000} ticks={3} />); // 500ms

      // 0ms, 250ms, 500ms
      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('250.00ms')).toBeInTheDocument();
      expect(screen.getByText('500.00ms')).toBeInTheDocument();
    });

    it('formats multi-second durations in seconds', () => {
      render(<TimeAxis durationUs={2000000} ticks={3} />); // 2s

      // 0ms, 1s, 2s
      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('1.00s')).toBeInTheDocument();
      expect(screen.getByText('2.00s')).toBeInTheDocument();
    });

    it('handles mixed formatting (ms to s transition)', () => {
      render(<TimeAxis durationUs={1500000} ticks={4} />); // 1.5s

      // 0ms, 500ms, 1s, 1.5s
      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('500.00ms')).toBeInTheDocument();
      expect(screen.getByText('1.00s')).toBeInTheDocument();
      expect(screen.getByText('1.50s')).toBeInTheDocument();
    });

    it('formats with decimal precision', () => {
      render(<TimeAxis durationUs={150000} ticks={3} />); // 150ms

      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('75.00ms')).toBeInTheDocument();
      expect(screen.getByText('150.00ms')).toBeInTheDocument();
    });
  });

  describe('Edge Cases', () => {
    it('handles zero duration', () => {
      render(<TimeAxis durationUs={0} />);

      // Should show single 0ms marker
      expect(screen.getByText('0ms')).toBeInTheDocument();
    });

    it('handles negative duration', () => {
      render(<TimeAxis durationUs={-1000} />);

      // Should fall back to showing 0ms
      expect(screen.getByText('0ms')).toBeInTheDocument();
    });

    it('handles very small duration', () => {
      render(<TimeAxis durationUs={100} ticks={5} />); // 0.1ms

      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('0.10ms')).toBeInTheDocument();
    });

    it('handles very large duration', () => {
      render(<TimeAxis durationUs={600000000} ticks={3} />); // 600s = 10 minutes

      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('5.00m')).toBeInTheDocument();
      expect(screen.getByText('10.00m')).toBeInTheDocument();
    });
  });

  describe('CSS Classes', () => {
    it('applies default styling classes', () => {
      const { container } = render(<TimeAxis durationUs={100000} />);
      const axis = container.firstChild as HTMLElement;

      expect(axis).toHaveClass('relative');
      expect(axis).toHaveClass('h-6');
      expect(axis).toHaveClass('border-b');
      expect(axis).toHaveClass('mb-2');
    });

    it('applies custom className', () => {
      const { container } = render(
        <TimeAxis durationUs={100000} className="custom-class" />
      );
      const axis = container.firstChild as HTMLElement;

      expect(axis).toHaveClass('custom-class');
    });

    it('combines default and custom classes', () => {
      const { container } = render(
        <TimeAxis durationUs={100000} className="my-custom-style" />
      );
      const axis = container.firstChild as HTMLElement;

      expect(axis).toHaveClass('relative');
      expect(axis).toHaveClass('my-custom-style');
    });
  });

  describe('Tick Positioning', () => {
    it('positions first tick at 0%', () => {
      const { container } = render(<TimeAxis durationUs={100000} ticks={5} />);
      const ticks = container.querySelectorAll('.absolute.top-0');

      // First tick should be at 0%
      expect(ticks[0]).toHaveStyle({ left: '0%' });
    });

    it('positions last tick at 100%', () => {
      const { container } = render(<TimeAxis durationUs={100000} ticks={5} />);
      const ticks = container.querySelectorAll('.absolute.top-0');

      // Last tick should be at 100%
      expect(ticks[ticks.length - 1]).toHaveStyle({ left: '100%' });
    });

    it('positions middle tick at 50%', () => {
      const { container } = render(<TimeAxis durationUs={100000} ticks={3} />);
      const ticks = container.querySelectorAll('.absolute.top-0');

      // Middle tick (index 1) should be at 50%
      expect(ticks[1]).toHaveStyle({ left: '50%' });
    });

    it('evenly distributes tick marks', () => {
      const { container } = render(<TimeAxis durationUs={100000} ticks={5} />);
      const ticks = container.querySelectorAll('.absolute.top-0');

      expect(ticks[0]).toHaveStyle({ left: '0%' });
      expect(ticks[1]).toHaveStyle({ left: '25%' });
      expect(ticks[2]).toHaveStyle({ left: '50%' });
      expect(ticks[3]).toHaveStyle({ left: '75%' });
      expect(ticks[4]).toHaveStyle({ left: '100%' });
    });
  });

  describe('TimeAxisCompact', () => {
    it('renders with 3 ticks by default', () => {
      render(<TimeAxisCompact durationUs={100000} />); // 100ms

      // Should render exactly 3 tick marks: 0ms, 50ms, 100ms
      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('50.00ms')).toBeInTheDocument();
      expect(screen.getByText('100.00ms')).toBeInTheDocument();

      // Should not render more ticks
      expect(screen.queryByText('25.00ms')).not.toBeInTheDocument();
      expect(screen.queryByText('75.00ms')).not.toBeInTheDocument();
    });

    it('applies custom className', () => {
      const { container } = render(
        <TimeAxisCompact durationUs={100000} className="compact-style" />
      );
      const axis = container.firstChild as HTMLElement;

      expect(axis).toHaveClass('compact-style');
    });

    it('shows correct time range', () => {
      render(<TimeAxisCompact durationUs={3000000} />); // 3s

      expect(screen.getByText('0.00ms')).toBeInTheDocument();
      expect(screen.getByText('1.50s')).toBeInTheDocument();
      expect(screen.getByText('3.00s')).toBeInTheDocument();
    });
  });

  describe('Layout Structure', () => {
    it('includes label area placeholder', () => {
      const { container } = render(<TimeAxis durationUs={100000} />);
      const labelArea = container.querySelector('.w-64.flex-shrink-0');

      expect(labelArea).toBeInTheDocument();
    });

    it('includes timeline area', () => {
      const { container } = render(<TimeAxis durationUs={100000} />);
      const timelineArea = container.querySelector('.absolute.left-64.right-0');

      expect(timelineArea).toBeInTheDocument();
    });

    it('renders tick marks with visual elements', () => {
      const { container } = render(<TimeAxis durationUs={100000} ticks={3} />);
      const tickMarks = container.querySelectorAll('.w-px.h-2.bg-slate-600');

      expect(tickMarks.length).toBe(3);
    });
  });
});
