import { useMemo } from 'react';
import { formatDuration } from '../../utils/traceUtils';

interface TimeAxisProps {
  /** Total duration in microseconds */
  durationUs: number;
  /** Number of tick marks to display (default: 5) */
  ticks?: number;
  /** Additional CSS classes */
  className?: string;
}

/**
 * Time ruler component for the waterfall view.
 *
 * Displays evenly spaced time markers (0ms, 50ms, 100ms, etc.) across the
 * top of the waterfall timeline. The number of ticks and their values are
 * calculated based on the total trace duration.
 *
 * @example
 * ```tsx
 * <TimeAxis durationUs={150000} /> // Shows 0ms, 37.5ms, 75ms, 112.5ms, 150ms
 * ```
 */
export function TimeAxis({ durationUs, ticks = 5, className = '' }: TimeAxisProps) {
  const tickMarks = useMemo(() => {
    if (durationUs <= 0 || ticks < 2) {
      return [{ percent: 0, label: '0ms' }];
    }

    const marks: { percent: number; label: string }[] = [];
    const interval = 100 / (ticks - 1);
    const durationInterval = durationUs / (ticks - 1);

    for (let i = 0; i < ticks; i++) {
      const percent = i * interval;
      const timeUs = i * durationInterval;
      marks.push({
        percent,
        label: formatDuration(timeUs),
      });
    }

    return marks;
  }, [durationUs, ticks]);

  return (
    <div
      className={`relative h-6 border-b border-slate-700/50 mb-2 ${className}`}
      role="presentation"
      aria-hidden="true"
    >
      {/* Label area placeholder (matches waterfall row label width) */}
      <div className="w-64 flex-shrink-0 inline-block" />

      {/* Timeline area */}
      <div className="absolute left-64 right-0 h-full">
        {tickMarks.map((tick, index) => (
          <div
            key={index}
            className="absolute top-0 h-full flex flex-col items-center"
            style={{ left: `${tick.percent}%` }}
          >
            {/* Tick mark */}
            <div className="w-px h-2 bg-slate-600" />
            {/* Label */}
            <span
              className={`text-xs text-slate-500 whitespace-nowrap ${
                index === tickMarks.length - 1 ? '-translate-x-full' : index === 0 ? '' : '-translate-x-1/2'
              }`}
            >
              {tick.label}
            </span>
          </div>
        ))}

        {/* Background grid lines */}
        <div className="absolute inset-0 flex justify-between pointer-events-none">
          {tickMarks.slice(1, -1).map((tick, index) => (
            <div
              key={index}
              className="w-px h-full bg-slate-700/30"
              style={{ position: 'absolute', left: `${tick.percent}%` }}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

/**
 * Compact time axis for smaller displays
 */
export function TimeAxisCompact({ durationUs, className = '' }: Omit<TimeAxisProps, 'ticks'>) {
  return <TimeAxis durationUs={durationUs} ticks={3} className={className} />;
}
