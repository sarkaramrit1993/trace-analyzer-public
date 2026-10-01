import React from 'react';
import type { Service } from '../../types';

/**
 * Filter values for the sidebar filters
 */
export interface SidebarFilters {
  serviceSearch: string;
  scope1Filter: string;
  scope2Filter: string;
  scope3Filter: string;
  envFilter: string;
  serviceIdentityFilter: string;
  filterErrors: boolean;
  filterDeviations: boolean;
  filterAnomalies: boolean;
}

/**
 * Filter options for the dropdown selects
 */
export interface SidebarFilterOptions {
  scope1Options: string[];
  scope2Options: string[];
  scope3Options: string[];
  envOptions: string[];
}

/**
 * Props for the Sidebar component
 */
export interface SidebarProps {
  services: Service[];
  selectedService: string | null;
  onServiceSelect: (serviceId: string) => void;
  filters: SidebarFilters;
  onFilterChange: (filterName: keyof SidebarFilters, value: string | boolean) => void;
  filterOptions: SidebarFilterOptions;
}

/**
 * Props for the ServiceItem sub-component
 */
interface ServiceItemProps {
  service: Service;
  isSelected: boolean;
  onSelect: (serviceId: string) => void;
}

/**
 * ServiceItem renders a single service in the sidebar list
 */
function ServiceItem({ service, isSelected, onSelect }: ServiceItemProps) {
  const handleClick = () => {
    onSelect(service.service_id);
  };

  const handleKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onSelect(service.service_id);
    }
  };

  const handleOpenInNewTab = (event: React.MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    const url = new URL(window.location.href);
    url.searchParams.set('service_id', service.service_id);
    url.hash = '';
    window.open(url.href, '_blank', 'noopener,noreferrer');
  };

  const title = service.service_grouping
    ? Object.entries(service.service_grouping)
        .filter(([, v]) => v)
        .map(([k, v]) => `${k}=${v}`)
        .join(' · ')
    : '';

  const scopePath =
    (service.service_grouping?.scope1 || '-') +
    ' / ' +
    (service.service_grouping?.scope2 || '-') +
    ' / ' +
    (service.service_grouping?.scope3 || '-');

  const envSuffix = service.service_grouping?.env
    ? ` · env=${service.service_grouping.env}`
    : '';

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      title={title}
      className={`w-full text-left px-3 py-2 rounded-lg text-sm transition-all cursor-pointer ${
        isSelected
          ? 'bg-emerald-500/20 text-emerald-400 border border-emerald-500/30'
          : 'text-slate-300 hover:bg-slate-700/50 border border-transparent'
      }`}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="font-medium truncate">
          {service.service_grouping?.service_identity || service.service_id}
        </div>
        <button
          onClick={handleOpenInNewTab}
          className="text-[11px] px-1.5 py-0.5 rounded border border-slate-700/50 text-slate-400 hover:text-slate-200 hover:border-slate-500/60"
          title="Open in new tab"
        >
          ↗
        </button>
      </div>
      <div className="text-[11px] text-slate-500 truncate">
        {scopePath}
        {envSuffix}
      </div>
      <div className="text-[11px] text-slate-600 truncate">
        id: {service.service_id}
      </div>
      <div className="text-xs text-slate-500">
        {service.trace_count} traces · {service.topology_count} topologies
      </div>
      <div className="text-xs text-slate-500">
        {service.deviation_count || 0} deviations · {service.anomaly_count || 0} anomalies
      </div>
    </div>
  );
}

/**
 * FilterSelect renders a labeled dropdown select for filtering
 */
interface FilterSelectProps {
  id: string;
  label: string;
  value: string;
  options: string[];
  onChange: (value: string) => void;
}

function FilterSelect({ id, label, value, options, onChange }: FilterSelectProps) {
  return (
    <div className="space-y-1">
      <label htmlFor={id} className="text-[11px] uppercase text-slate-500 tracking-wide">
        {label}
      </label>
      <select
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full bg-slate-800/60 border border-slate-700/50 rounded-md px-2 py-1 text-sm text-slate-200 focus:outline-none focus:ring-1 focus:ring-emerald-500/50"
      >
        <option value="">All</option>
        {options.map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
    </div>
  );
}

/**
 * FilterCheckbox renders a labeled checkbox for boolean filters
 */
interface FilterCheckboxProps {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}

function FilterCheckbox({ label, checked, onChange }: FilterCheckboxProps) {
  return (
    <label className="flex items-center gap-2 text-xs text-slate-400">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
      />
      {label}
    </label>
  );
}

/**
 * Sidebar component for filtering and selecting services
 */
export function Sidebar({
  services,
  selectedService,
  onServiceSelect,
  filters,
  onFilterChange,
  filterOptions,
}: SidebarProps) {
  return (
    <aside className="w-64 bg-slate-800/30 backdrop-blur-sm border-r border-slate-700/50 h-full flex flex-col">
      <div className="p-4 flex-shrink-0">
        <h2 className="text-sm font-semibold text-slate-400 uppercase mb-3">Services</h2>
        <div className="space-y-2">
          {/* Search input */}
          <input
            value={filters.serviceSearch}
            onChange={(e) => onFilterChange('serviceSearch', e.target.value)}
            placeholder="Filter services..."
            className="w-full bg-slate-800/60 border border-slate-700/50 rounded-md px-2 py-1 text-sm text-slate-200 placeholder:text-slate-500 focus:outline-none focus:ring-1 focus:ring-emerald-500/50"
          />

          {/* Scope and Env filters */}
          <FilterSelect
            id="scope1-filter"
            label="Scope1"
            value={filters.scope1Filter}
            options={filterOptions.scope1Options}
            onChange={(value) => onFilterChange('scope1Filter', value)}
          />
          <FilterSelect
            id="scope2-filter"
            label="Scope2"
            value={filters.scope2Filter}
            options={filterOptions.scope2Options}
            onChange={(value) => onFilterChange('scope2Filter', value)}
          />
          <FilterSelect
            id="scope3-filter"
            label="Scope3"
            value={filters.scope3Filter}
            options={filterOptions.scope3Options}
            onChange={(value) => onFilterChange('scope3Filter', value)}
          />
          <FilterSelect
            id="env-filter"
            label="Env"
            value={filters.envFilter}
            options={filterOptions.envOptions}
            onChange={(value) => onFilterChange('envFilter', value)}
          />

          {/* Service Identity filter */}
          <div className="space-y-1">
            <label
              htmlFor="service-identity-filter"
              className="text-[11px] uppercase text-slate-500 tracking-wide"
            >
              Service Identity
            </label>
            <input
              id="service-identity-filter"
              value={filters.serviceIdentityFilter}
              onChange={(e) => onFilterChange('serviceIdentityFilter', e.target.value)}
              placeholder="Exact match..."
              className="w-full bg-slate-800/60 border border-slate-700/50 rounded-md px-2 py-1 text-sm text-slate-200 placeholder:text-slate-500 focus:outline-none focus:ring-1 focus:ring-emerald-500/50"
            />
          </div>

          {/* Boolean filter checkboxes */}
          <FilterCheckbox
            label="Errors only"
            checked={filters.filterErrors}
            onChange={(checked) => onFilterChange('filterErrors', checked)}
          />
          <FilterCheckbox
            label="Deviations only"
            checked={filters.filterDeviations}
            onChange={(checked) => onFilterChange('filterDeviations', checked)}
          />
          <FilterCheckbox
            label="Anomalies only"
            checked={filters.filterAnomalies}
            onChange={(checked) => onFilterChange('filterAnomalies', checked)}
          />
        </div>
      </div>

      {/* Scrollable services list */}
      <div className="flex-1 overflow-y-auto px-4 pb-4">
        <div className="space-y-1">
          {services.map((service) => (
            <ServiceItem
              key={service.service_id}
              service={service}
              isSelected={selectedService === service.service_id}
              onSelect={onServiceSelect}
            />
          ))}
          {services.length === 0 && (
            <div className="text-slate-500 text-sm px-3 py-2">Waiting for traces...</div>
          )}
        </div>
      </div>
    </aside>
  );
}
