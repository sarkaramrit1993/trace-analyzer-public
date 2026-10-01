import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { Card } from './Card';
import { Stat } from './Stat';
import { MetricCard } from './MetricCard';
import { HelpIcon } from './HelpIcon';

describe('Card', () => {
  describe('Title Rendering', () => {
    it('renders with title', () => {
      render(
        <Card title="Test Card Title">
          <p>Card content</p>
        </Card>
      );
      expect(screen.getByText('Test Card Title')).toBeInTheDocument();
    });

    it('renders title as h3 element', () => {
      render(
        <Card title="Test Title">
          <p>Content</p>
        </Card>
      );
      const title = screen.getByText('Test Title');
      expect(title.tagName).toBe('H3');
    });

    it('renders title with uppercase styling', () => {
      render(
        <Card title="Test Title">
          <p>Content</p>
        </Card>
      );
      const title = screen.getByText('Test Title');
      expect(title).toHaveClass('uppercase');
    });
  });

  describe('Children Rendering', () => {
    it('renders children content', () => {
      render(
        <Card title="Card">
          <p>Child paragraph content</p>
        </Card>
      );
      expect(screen.getByText('Child paragraph content')).toBeInTheDocument();
    });

    it('renders multiple children elements', () => {
      render(
        <Card title="Card">
          <p>First child</p>
          <span>Second child</span>
          <div>Third child</div>
        </Card>
      );
      expect(screen.getByText('First child')).toBeInTheDocument();
      expect(screen.getByText('Second child')).toBeInTheDocument();
      expect(screen.getByText('Third child')).toBeInTheDocument();
    });

    it('renders complex nested children', () => {
      render(
        <Card title="Card">
          <div>
            <ul>
              <li>Item 1</li>
              <li>Item 2</li>
            </ul>
          </div>
        </Card>
      );
      expect(screen.getByText('Item 1')).toBeInTheDocument();
      expect(screen.getByText('Item 2')).toBeInTheDocument();
    });
  });

  describe('Help Text', () => {
    it('renders HelpIcon when helpText is provided', () => {
      render(
        <Card title="Card" helpText="This is help text">
          <p>Content</p>
        </Card>
      );
      // HelpIcon renders a button with aria-label "Help"
      expect(screen.getByRole('button', { name: 'Help' })).toBeInTheDocument();
    });

    it('does not render HelpIcon when helpText is not provided', () => {
      render(
        <Card title="Card">
          <p>Content</p>
        </Card>
      );
      expect(screen.queryByRole('button', { name: 'Help' })).not.toBeInTheDocument();
    });

    it('shows help tooltip content on hover', async () => {
      render(
        <Card title="Card" helpText="Helpful information here">
          <p>Content</p>
        </Card>
      );

      const helpButton = screen.getByRole('button', { name: 'Help' });
      fireEvent.mouseEnter(helpButton);

      await waitFor(() => {
        expect(screen.getByText('Helpful information here')).toBeInTheDocument();
      });
    });
  });

  describe('Styling', () => {
    it('renders with card container styling', () => {
      render(
        <Card title="Card">
          <p>Content</p>
        </Card>
      );
      const card = screen.getByText('Content').closest('div.bg-slate-800\\/30');
      expect(card).toBeInTheDocument();
    });
  });
});

describe('Stat', () => {
  describe('Label and Value Rendering', () => {
    it('renders label and value', () => {
      render(<Stat label="Total Count" value={150} />);
      expect(screen.getByText('Total Count')).toBeInTheDocument();
      expect(screen.getByText('150')).toBeInTheDocument();
    });

    it('renders string value correctly', () => {
      render(<Stat label="Status" value="Active" />);
      expect(screen.getByText('Status')).toBeInTheDocument();
      expect(screen.getByText('Active')).toBeInTheDocument();
    });

    it('renders numeric value correctly', () => {
      render(<Stat label="Count" value={42} />);
      expect(screen.getByText('42')).toBeInTheDocument();
    });

    it('renders zero value', () => {
      render(<Stat label="Errors" value={0} />);
      expect(screen.getByText('0')).toBeInTheDocument();
    });

    it('renders label below value', () => {
      render(<Stat label="Test Label" value="Test Value" />);
      const value = screen.getByText('Test Value');
      const label = screen.getByText('Test Label');

      // Value should have lg font, label should have xs font
      expect(value).toHaveClass('text-lg');
      expect(label).toHaveClass('text-xs');
    });
  });

  describe('Variant Handling', () => {
    it('handles default variant with emerald color', () => {
      render(<Stat label="Label" value="Value" />);
      const value = screen.getByText('Value');
      expect(value).toHaveClass('text-emerald-400');
    });

    it('handles explicit default variant', () => {
      render(<Stat label="Label" value="Value" variant="default" />);
      const value = screen.getByText('Value');
      expect(value).toHaveClass('text-emerald-400');
    });

    it('handles warning variant with amber color', () => {
      render(<Stat label="Warnings" value={5} variant="warning" />);
      const value = screen.getByText('5');
      expect(value).toHaveClass('text-amber-400');
    });

    it('handles error variant with red color', () => {
      render(<Stat label="Errors" value={3} variant="error" />);
      const value = screen.getByText('3');
      expect(value).toHaveClass('text-red-400');
    });
  });

  describe('Large Numbers', () => {
    it('renders large numbers correctly', () => {
      render(<Stat label="Total" value={1000000} />);
      expect(screen.getByText('1000000')).toBeInTheDocument();
    });

    it('renders formatted string numbers', () => {
      render(<Stat label="Total" value="1,000,000" />);
      expect(screen.getByText('1,000,000')).toBeInTheDocument();
    });

    it('renders decimal numbers', () => {
      render(<Stat label="Average" value={99.99} />);
      expect(screen.getByText('99.99')).toBeInTheDocument();
    });

    it('renders percentage strings', () => {
      render(<Stat label="Rate" value="45.5%" />);
      expect(screen.getByText('45.5%')).toBeInTheDocument();
    });
  });

  describe('Styling', () => {
    it('has centered text alignment', () => {
      render(<Stat label="Label" value="Value" />);
      const container = screen.getByText('Value').parentElement;
      expect(container).toHaveClass('text-center');
    });

    it('value has bold font weight', () => {
      render(<Stat label="Label" value="Value" />);
      const value = screen.getByText('Value');
      expect(value).toHaveClass('font-bold');
    });
  });
});

describe('MetricCard', () => {
  describe('Title and Value Rendering', () => {
    it('renders title (label) and value', () => {
      render(<MetricCard label="Response Time" value="250ms" />);
      expect(screen.getByText('Response Time')).toBeInTheDocument();
      expect(screen.getByText('250ms')).toBeInTheDocument();
    });

    it('renders label as uppercase', () => {
      render(<MetricCard label="Latency" value="100ms" />);
      const label = screen.getByText('Latency');
      expect(label).toHaveClass('uppercase');
    });

    it('renders numeric value correctly', () => {
      render(<MetricCard label="Count" value={1500} />);
      expect(screen.getByText('1500')).toBeInTheDocument();
    });

    it('renders zero value', () => {
      render(<MetricCard label="Errors" value={0} />);
      expect(screen.getByText('0')).toBeInTheDocument();
    });
  });

  describe('Subtitle/Comparison Rendering', () => {
    it('shows subtitle when provided', () => {
      render(
        <MetricCard
          label="Response Time"
          value="250ms"
          subtitle="15% faster than baseline"
        />
      );
      expect(screen.getByText('15% faster than baseline')).toBeInTheDocument();
    });

    it('does not render subtitle when not provided', () => {
      const { container } = render(<MetricCard label="Metric" value="100" />);
      // Check there's no subtitle div
      const subtitleDiv = container.querySelector('div.text-\\[10px\\]');
      expect(subtitleDiv).not.toBeInTheDocument();
    });

    it('does not render subtitle when null', () => {
      const { container } = render(
        <MetricCard label="Metric" value="100" subtitle={null} />
      );
      const subtitleDiv = container.querySelector('div.text-\\[10px\\]');
      expect(subtitleDiv).not.toBeInTheDocument();
    });

    it('handles positive comparison subtitle', () => {
      render(
        <MetricCard
          label="Performance"
          value="95%"
          subtitle="+10% from last week"
        />
      );
      expect(screen.getByText('+10% from last week')).toBeInTheDocument();
    });

    it('handles negative comparison subtitle', () => {
      render(
        <MetricCard
          label="Error Rate"
          value="2%"
          subtitle="-5% from last week"
        />
      );
      expect(screen.getByText('-5% from last week')).toBeInTheDocument();
    });
  });

  describe('Variant Handling', () => {
    it('handles default variant with emerald color', () => {
      render(<MetricCard label="Metric" value="100" />);
      const value = screen.getByText('100');
      expect(value).toHaveClass('text-emerald-400');
    });

    it('handles success variant with emerald color', () => {
      render(<MetricCard label="Metric" value="100" variant="success" />);
      const value = screen.getByText('100');
      expect(value).toHaveClass('text-emerald-400');
    });

    it('handles error variant with red color', () => {
      render(<MetricCard label="Failures" value={10} variant="error" />);
      const value = screen.getByText('10');
      expect(value).toHaveClass('text-red-400');
    });

    it('handles warning variant with amber color', () => {
      render(<MetricCard label="Warnings" value={5} variant="warning" />);
      const value = screen.getByText('5');
      expect(value).toHaveClass('text-amber-400');
    });
  });

  describe('Styling', () => {
    it('renders with card background styling', () => {
      const { container } = render(<MetricCard label="Metric" value="100" />);
      const card = container.querySelector('div.bg-slate-700\\/30');
      expect(card).toBeInTheDocument();
    });

    it('value has extra-large font size', () => {
      render(<MetricCard label="Metric" value="100" />);
      const value = screen.getByText('100');
      expect(value).toHaveClass('text-xl');
    });

    it('value has bold font weight', () => {
      render(<MetricCard label="Metric" value="100" />);
      const value = screen.getByText('100');
      expect(value).toHaveClass('font-bold');
    });

    it('subtitle has small font size', () => {
      render(<MetricCard label="Metric" value="100" subtitle="comparison text" />);
      const subtitle = screen.getByText('comparison text');
      expect(subtitle).toHaveClass('text-[10px]');
    });
  });
});

describe('HelpIcon', () => {
  describe('Tooltip Trigger Rendering', () => {
    it('renders tooltip trigger button', () => {
      render(<HelpIcon text="Help text content" />);
      expect(screen.getByRole('button', { name: 'Help' })).toBeInTheDocument();
    });

    it('renders help icon SVG', () => {
      const { container } = render(<HelpIcon text="Help text" />);
      const svg = container.querySelector('svg');
      expect(svg).toBeInTheDocument();
    });

    it('button has cursor-help styling', () => {
      render(<HelpIcon text="Help text" />);
      const button = screen.getByRole('button', { name: 'Help' });
      expect(button).toHaveClass('cursor-help');
    });

    it('has aria-label for accessibility', () => {
      render(<HelpIcon text="Help text" />);
      const button = screen.getByRole('button', { name: 'Help' });
      expect(button).toHaveAttribute('aria-label', 'Help');
    });
  });

  describe('Tooltip Content on Hover', () => {
    it('shows tooltip content on mouse enter', async () => {
      render(<HelpIcon text="This is the tooltip content" />);
      const button = screen.getByRole('button', { name: 'Help' });

      // Tooltip should not be visible initially
      expect(screen.queryByText('This is the tooltip content')).not.toBeInTheDocument();

      fireEvent.mouseEnter(button);

      await waitFor(() => {
        expect(screen.getByText('This is the tooltip content')).toBeInTheDocument();
      });
    });

    it('hides tooltip content on mouse leave', async () => {
      render(<HelpIcon text="Tooltip text" />);
      const button = screen.getByRole('button', { name: 'Help' });

      fireEvent.mouseEnter(button);

      await waitFor(() => {
        expect(screen.getByText('Tooltip text')).toBeInTheDocument();
      });

      fireEvent.mouseLeave(button);

      await waitFor(() => {
        expect(screen.queryByText('Tooltip text')).not.toBeInTheDocument();
      });
    });

    it('shows tooltip content on click', async () => {
      render(<HelpIcon text="Clickable tooltip" />);
      const button = screen.getByRole('button', { name: 'Help' });

      fireEvent.click(button);

      await waitFor(() => {
        expect(screen.getByText('Clickable tooltip')).toBeInTheDocument();
      });
    });

    it('toggles tooltip on click', async () => {
      render(<HelpIcon text="Toggle tooltip" />);
      const button = screen.getByRole('button', { name: 'Help' });

      // First click - show
      fireEvent.click(button);
      await waitFor(() => {
        expect(screen.getByText('Toggle tooltip')).toBeInTheDocument();
      });

      // Second click - hide
      fireEvent.click(button);
      await waitFor(() => {
        expect(screen.queryByText('Toggle tooltip')).not.toBeInTheDocument();
      });
    });
  });

  describe('Tooltip Position and Styling', () => {
    it('tooltip has absolute positioning', async () => {
      render(<HelpIcon text="Positioned tooltip" />);
      const button = screen.getByRole('button', { name: 'Help' });

      fireEvent.mouseEnter(button);

      await waitFor(() => {
        const tooltip = screen.getByText('Positioned tooltip');
        expect(tooltip).toHaveClass('absolute');
      });
    });

    it('tooltip has high z-index', async () => {
      render(<HelpIcon text="High z-index tooltip" />);
      const button = screen.getByRole('button', { name: 'Help' });

      fireEvent.mouseEnter(button);

      await waitFor(() => {
        const tooltip = screen.getByText('High z-index tooltip');
        expect(tooltip).toHaveClass('z-50');
      });
    });

    it('tooltip is positioned at left-0 top-6', async () => {
      render(<HelpIcon text="Left-positioned tooltip" />);
      const button = screen.getByRole('button', { name: 'Help' });

      fireEvent.mouseEnter(button);

      await waitFor(() => {
        const tooltip = screen.getByText('Left-positioned tooltip');
        expect(tooltip).toHaveClass('left-0');
        expect(tooltip).toHaveClass('top-6');
      });
    });

    it('tooltip has backdrop blur effect', async () => {
      render(<HelpIcon text="Blurred tooltip" />);
      const button = screen.getByRole('button', { name: 'Help' });

      fireEvent.mouseEnter(button);

      await waitFor(() => {
        const tooltip = screen.getByText('Blurred tooltip');
        expect(tooltip).toHaveClass('backdrop-blur-sm');
      });
    });

    it('tooltip is not interactive (pointer-events-none)', async () => {
      render(<HelpIcon text="Non-interactive tooltip" />);
      const button = screen.getByRole('button', { name: 'Help' });

      fireEvent.mouseEnter(button);

      await waitFor(() => {
        const tooltip = screen.getByText('Non-interactive tooltip');
        expect(tooltip).toHaveClass('pointer-events-none');
      });
    });

    it('tooltip has fixed width', async () => {
      render(<HelpIcon text="Fixed width tooltip" />);
      const button = screen.getByRole('button', { name: 'Help' });

      fireEvent.mouseEnter(button);

      await waitFor(() => {
        const tooltip = screen.getByText('Fixed width tooltip');
        expect(tooltip).toHaveClass('w-64');
      });
    });
  });

  describe('Edge Cases', () => {
    it('handles empty text prop', () => {
      render(<HelpIcon text="" />);
      const button = screen.getByRole('button', { name: 'Help' });

      fireEvent.mouseEnter(button);
      // Should not crash, tooltip might be empty or not render
      expect(button).toBeInTheDocument();
    });

    it('handles long text content', async () => {
      const longText = 'This is a very long help text that explains something in detail. '.repeat(5);
      render(<HelpIcon text={longText} />);
      const button = screen.getByRole('button', { name: 'Help' });

      fireEvent.mouseEnter(button);

      await waitFor(() => {
        // Use partial text match for long content
        expect(screen.getByText(/This is a very long help text/)).toBeInTheDocument();
      });
    });

    it('handles text with special characters', async () => {
      render(<HelpIcon text="Help text with <special> & 'characters'" />);
      const button = screen.getByRole('button', { name: 'Help' });

      fireEvent.mouseEnter(button);

      await waitFor(() => {
        expect(screen.getByText("Help text with <special> & 'characters'")).toBeInTheDocument();
      });
    });
  });
});
