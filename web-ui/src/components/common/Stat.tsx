type StatVariant = 'default' | 'warning' | 'error';

interface StatProps {
  label: string;
  value: string | number;
  variant?: StatVariant;
}

export function Stat({ label, value, variant = 'default' }: StatProps) {
  const colors: Record<StatVariant, string> = {
    default: 'text-emerald-400',
    warning: 'text-amber-400',
    error: 'text-red-400'
  };

  return (
    <div className="text-center">
      <div className={`text-lg font-bold ${colors[variant]}`}>{value}</div>
      <div className="text-slate-500 text-xs">{label}</div>
    </div>
  );
}
