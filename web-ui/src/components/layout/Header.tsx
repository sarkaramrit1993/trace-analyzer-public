import { useState } from 'react';
import { Stat } from '../common';
import type { Stats, CombinationInfo, BaselineVariant } from '../../types';

interface VariantTooltip {
  title: string;
  description: string;
  calculation: string;
  window: string;
  bestFor: string;
  link: string;
}

function getVariantTooltip(variantName: string): VariantTooltip {
  switch (variantName) {
    case 'cumulative':
      return {
        title: 'Cumulative Baseline',
        description: `Accumulates all historical data with equal weight. Stable and comprehensive, but slower to adapt to changes.`,
        calculation: `• Frequency: Count of each topology / Total traces (all time)
• Duration: Reservoir sampling (1000 samples) for percentiles
• Updates: Every trace increments counts, samples stored via reservoir sampling
• Minimum traces for baseline: 50 traces per topology`,
        window: `Window: All historical data (no expiration)
Recommended threshold: 50-100 traces for stable baselines`,
        bestFor: 'Stable systems where historical patterns matter more than recent changes',
        link: 'https://en.wikipedia.org/wiki/Moving_average#Simple_moving_average'
      };

    case 'ewma':
      return {
        title: 'EWMA (Exponentially Weighted Moving Average)',
        description: `Gives more weight to recent data while retaining history. Adapts quickly to changes while maintaining stability.`,
        calculation: `• Frequency: EWMA formula: new_freq = α × current + (1-α) × previous
  - α (alpha) = 0.1 means recent data gets 10% weight per update
  - Older data gradually decays but never fully disappears
• Duration: EWMA smoothing on mean duration per path
• Updates: Each trace updates frequency with exponential weighting`,
        window: `Window: Effectively infinite (all data, but weighted)
Recommended threshold: 500 traces (needs more data for meaningful weighting)
Alpha (α): 0.1 = recent data gets 10% weight, 90% from history`,
        bestFor: 'Systems with gradual changes or seasonal patterns',
        link: 'https://en.wikipedia.org/wiki/Moving_average#Exponential_moving_average'
      };

    case 'sliding_window':
      return {
        title: 'Sliding Window',
        description: `Only considers data from a fixed time window (default: 24 hours). Fast adaptation to changes, discards old data completely.`,
        calculation: `• Frequency: Counts within time window / Total in window
• Duration: Standard statistics (mean, percentiles) from window data
• Updates: Old data automatically expires after window duration
• Decay: Periodic decay (99% factor) to simulate window sliding`,
        window: `Window: 24 hours (configurable: 1h, 24h, 168h weekly, 720h monthly)
Recommended threshold: 50 traces (but time-gated by window)
Data older than window is discarded`,
        bestFor: 'Rapidly changing systems or recent anomaly detection',
        link: 'https://en.wikipedia.org/wiki/Window_function'
      };

    case 'exponential_decay':
      return {
        title: 'Exponential Decay',
        description: `Time-weighted exponential decay. Older data gradually loses influence based on age, with 24-hour half-life.`,
        calculation: `• Frequency: Weight = exp(-λ × age) where λ = ln(2) / 24h
  - Data from 24h ago has 50% weight
  - Data from 48h ago has 25% weight
  - Never fully discards, but very old data has minimal impact
• Duration: Time-weighted statistics with exponential decay
• Updates: Each trace applies decay factor based on time since last update`,
        window: `Half-life: 24 hours (data from 24h ago has 50% weight)
Recommended threshold: 5000 traces (needs substantial data for decay to be meaningful)
Effective window: ~72 hours (data older has <12.5% weight)`,
        bestFor: 'Balancing recent trends with historical context',
        link: 'https://en.wikipedia.org/wiki/Exponential_decay'
      };

    case 'same_time_yesterday':
      return {
        title: 'Same Time Yesterday',
        description: `Compares current traces to the same time yesterday (±2 hours). Skips weekends (uses Friday instead).`,
        calculation: `• Window: 4-hour window (±2 hours around same time yesterday)
• Weekend handling: If yesterday is Saturday/Sunday, uses Friday's baseline instead
• Comparison: Current traces compared to yesterday's baseline for deviations/anomalies
• Storage: Data stored in hourly buckets (UTC) for historical comparison`,
        window: `Window: 4-hour window (±2 hours around same time yesterday)
Recommended threshold: 50 traces per topology
Historical data: Stored in hourly buckets for day-over-day comparison`,
        bestFor: 'Day-over-day comparison, detecting daily pattern changes',
        link: 'https://en.wikipedia.org/wiki/Time_series#Seasonality'
      };

    default:
      return { title: variantName, description: '', calculation: '', window: '', bestFor: '', link: '' };
  }
}

export interface HeaderProps {
  stats: Stats;
  autoRefresh: boolean;
  onAutoRefreshToggle: () => void;
  combinations: CombinationInfo[];
  activeCombination: string;
  onCombinationSwitch: (combination: string) => void;
  showBaselinePanel: boolean;
  onToggleBaselinePanel: () => void;
}

const VARIANT_NAMES: BaselineVariant[] = [
  'cumulative',
  'ewma',
  'sliding_window',
  'exponential_decay',
  'same_time_yesterday'
];

const VARIANT_LABELS: Record<BaselineVariant, string> = {
  cumulative: 'Cumulative',
  ewma: 'EWMA',
  sliding_window: 'Sliding Window',
  exponential_decay: 'Exponential Decay',
  same_time_yesterday: 'Same Time Yesterday'
};

export function Header({
  stats,
  autoRefresh,
  onAutoRefreshToggle,
  combinations,
  activeCombination,
  onCombinationSwitch,
  showBaselinePanel,
  onToggleBaselinePanel
}: HeaderProps) {
  const [hoveredVariant, setHoveredVariant] = useState<string | null>(null);

  return (
    <header className="bg-slate-800/50 backdrop-blur-sm border-b border-slate-700/50 px-6 py-3 sticky top-0 z-10">
      <div className="flex items-center justify-between mb-2">
        <h1 className="text-2xl font-bold text-emerald-400">Trace Deviation Analyzer</h1>
        {stats.active_variant && (
          <span className="text-xs text-gray-400 ml-2">
            Variant: {stats.active_variant}
          </span>
        )}
        <div className="flex gap-6 text-sm">
          <Stat label="Traces" value={stats.hot_tier_traces || 0} />
          <Stat label="Services" value={stats.unique_services || 0} />
          <Stat label="Topologies" value={stats.unique_topologies || 0} />
          <Stat label="Deviations" value={stats.total_deviations || 0} variant="warning" />
          <Stat label="Anomalies" value={stats.total_anomalies || 0} variant="error" />
          <Stat label="Errors" value={stats.total_errors || 0} variant="error" />
          <Stat
            label="Interesting"
            value={`${stats.interesting_traces || 0} (${(stats.interesting_traces_pct || 0).toFixed(1)}%)`}
            variant={stats.interesting_traces_pct > 10 ? "error" : stats.interesting_traces_pct > 5 ? "warning" : "default"}
          />
          <button
            onClick={onAutoRefreshToggle}
            className={`px-2 py-1 rounded text-xs border ${
              autoRefresh
                ? 'bg-emerald-500/20 text-emerald-300 border-emerald-500/40'
                : 'bg-slate-700/40 text-slate-300 border-slate-600/40'
            }`}
            title={autoRefresh ? 'Auto-refresh on' : 'Auto-refresh off'}
          >
            Auto-refresh {autoRefresh ? 'On' : 'Off'}
          </button>
        </div>
      </div>
      {/* Combination Dropdowns for A/B Testing - 5 variants × 4 thresholds */}
      {combinations.length > 0 && (
        <div className="border-t border-slate-700/50 pt-3 mt-3">
          <button
            type="button"
            onClick={onToggleBaselinePanel}
            className="w-full flex items-center justify-between text-xs font-semibold text-slate-300 mb-2"
          >
            <span>Baseline Strategy & Threshold Selection</span>
            <span className="text-slate-400">{showBaselinePanel ? '▾' : '▸'}</span>
          </button>
          {showBaselinePanel && (
            <>
              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-3">
                {VARIANT_NAMES.map((variantName) => {
                  const variantCombinations = combinations.filter(c => c.variant === variantName);
                  const [activeVariant] = activeCombination.split(':');
                  const isVariantActive = activeVariant === variantName;

                  // Find the combination for this variant that matches the active threshold
                  const getCurrentValue = (): string => {
                    if (isVariantActive) {
                      return activeCombination;
                    }
                    // If not active, find the combination with same threshold as active
                    const [, activeThreshold] = activeCombination.split(':');
                    const matchingCombo = variantCombinations.find(c => {
                      const [, threshold] = c.combination.split(':');
                      return threshold === activeThreshold;
                    });
                    return matchingCombo ? matchingCombo.combination : (variantCombinations.length > 0 ? variantCombinations[0].combination : `${variantName}:50`);
                  };

                  const variantLabel = VARIANT_LABELS[variantName] || variantName;
                  const tooltip = getVariantTooltip(variantName);
                  const isHovered = hoveredVariant === variantName;

                  return (
                    <div
                      key={variantName}
                      className={`relative p-2.5 rounded-lg border transition-colors ${
                        isVariantActive
                          ? 'bg-emerald-500/10 border-emerald-500/40'
                          : 'bg-slate-800/30 border-slate-700/50'
                      }`}
                      onMouseEnter={() => setHoveredVariant(variantName)}
                      onMouseLeave={() => setHoveredVariant(null)}
                    >
                      {isHovered && (
                        <div className="absolute z-50 left-full ml-2 top-0 w-96 p-4 bg-slate-900 border border-slate-700 rounded-lg shadow-xl text-xs">
                          <div className="font-bold text-emerald-400 mb-2">{tooltip.title}</div>
                          <div className="text-slate-300 mb-3">{tooltip.description}</div>
                          <div className="text-slate-400 mb-2">
                            <div className="font-semibold text-slate-300 mb-1">How it calculates:</div>
                            <pre className="whitespace-pre-wrap text-[11px] font-mono">{tooltip.calculation}</pre>
                          </div>
                          <div className="text-slate-400 mb-2">
                            <div className="font-semibold text-slate-300 mb-1">Window & Threshold:</div>
                            <div className="text-[11px]">{tooltip.window}</div>
                          </div>
                          <div className="text-slate-400 mb-2">
                            <div className="font-semibold text-slate-300 mb-1">Best for:</div>
                            <div className="text-[11px]">{tooltip.bestFor}</div>
                          </div>
                          <a
                            href={tooltip.link}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="text-emerald-400 hover:text-emerald-300 text-[11px] underline"
                          >
                            Learn more →
                          </a>
                        </div>
                      )}
                      <label className="block text-[11px] font-semibold text-slate-300 mb-1.5 cursor-help">
                        {variantLabel}
                      </label>
                      <select
                        value={getCurrentValue()}
                        onChange={(e) => {
                          e.preventDefault();
                          e.stopPropagation();
                          onCombinationSwitch(e.target.value);
                        }}
                        onClick={(e) => {
                          e.stopPropagation();
                        }}
                        className={`w-full px-2.5 py-1.5 rounded text-xs font-medium transition-colors border cursor-pointer ${
                          isVariantActive
                            ? 'bg-emerald-500/20 text-emerald-200 border-emerald-500/50'
                            : 'bg-slate-700/60 text-slate-200 border-slate-600/50 hover:bg-slate-700/80'
                        } focus:outline-none focus:ring-2 focus:ring-emerald-500/50`}
                      >
                        {variantCombinations.map((combo) => {
                          const [, threshold] = combo.combination.split(':');
                          return (
                            <option key={combo.combination} value={combo.combination}>
                              {threshold} traces
                            </option>
                          );
                        })}
                      </select>
                      {isVariantActive && (
                        <div className="mt-1 text-[11px] text-emerald-400 font-medium">
                          ✓ Active
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
              {/* Active combination info */}
              {activeCombination && (
                <div className="mt-2 px-3 py-2 bg-slate-800/40 rounded border border-slate-700/50">
                  <div className="text-[11px] text-slate-400">Currently Active:</div>
                  <div className="text-xs font-semibold text-emerald-400 mt-1">
                    {activeCombination.split(':')[0].charAt(0).toUpperCase() + activeCombination.split(':')[0].slice(1).replace('_', ' ')} - {activeCombination.split(':')[1]} traces threshold
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      )}
    </header>
  );
}
