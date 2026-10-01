type MetricCardVariant = 'default' | 'success' | 'error' | 'warning';

interface MetricCardProps {
  label: string;
  value: string | number;
  variant?: MetricCardVariant;
  subtitle?: string | null;
}

export function MetricCard({ label, value, variant = 'default', subtitle = null }: MetricCardProps) {
  const colors: Record<MetricCardVariant, string> = {
    default: 'text-emerald-400',
    success: 'text-emerald-400',
    error: 'text-red-400',
    warning: 'text-amber-400'
  };

  return (
    <div className="bg-slate-700/30 rounded-lg p-3 border border-slate-600/30">
      <div className="text-slate-500 text-xs uppercase">{label}</div>
      <div className={`text-xl font-bold ${colors[variant]}`}>{value}</div>
      {subtitle && (
        <div className="text-slate-500 text-[10px] mt-1">{subtitle}</div>
      )}
    </div>
  );
}
