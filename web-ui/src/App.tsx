import { useState, useEffect, useRef } from 'react';
import { PieChart, Pie, Cell, Tooltip, ResponsiveContainer } from 'recharts';

// Component imports
import { Header } from './components/layout/Header';
import { Sidebar, SidebarFilters, SidebarFilterOptions } from './components/layout/Sidebar';
import { TraceDetailView } from './components/traces/TraceDetailView';
import { TopologyModal } from './components/topology/TopologyModal';
import { Card, MetricCard } from './components/common';
import { getJSON, postJSON, HttpError } from './api';
import { usePolling } from './hooks/usePolling';
import { buildGraphData } from './utils/graphData';

// Type imports
import type {
  Service,
  ServiceGrouping,
  Stats,
  TraceDetail,
  Deviation,
  Anomaly,
  TopologyInfo,
  CombinationsResponse,
  CombinationInfo,
  SetCombinationResponse,
} from './types';

// Extended topology type that includes trace data for the modal
interface SelectedTopology extends TopologyInfo {
  trace?: TraceDetail;
  serviceGrouping?: ServiceGrouping | null;
  errorMessage?: string | null;
}

const COLORS = ['#10B981', '#34D399', '#6EE7B7', '#A7F3D0', '#D1FAE5', '#ECFDF5'];

const UI_STATE_KEY = 'trace-analyzer-ui-state';
const EMPTY_TRACE = { spans: [] } as unknown as TraceDetail;

// The ?service_id= URL param wins when that service exists; otherwise keep the current pick or default to the first.
function pickService(current: string | null, services: Service[], urlServiceId: string | null): string | null {
  if (services.length === 0) return current;
  if (urlServiceId && services.some(svc => svc.service_id === urlServiceId)) return urlServiceId;
  return current || services[0].service_id;
}

// replaceState, not pushState: switching services shouldn't fill the back button.
function syncServiceIdParam(serviceId: string | null) {
  const url = new URL(window.location.href);
  if (serviceId) url.searchParams.set('service_id', serviceId);
  else url.searchParams.delete('service_id');
  if (url.href !== window.location.href) window.history.replaceState(window.history.state, '', url.href);
}

// Topology lookups fall through to the next strategy on HTTP errors; network errors still surface.
function orNullOnHttpError<T>(request: Promise<T>): Promise<T | null> {
  return request.catch(err => {
    if (err instanceof HttpError) return null;
    throw err;
  });
}


function App() {
  // Core state
  const [services, setServices] = useState<Service[]>([]);
  const [selectedService, setSelectedService] = useState<string | null>(null);
  // Until services load, the URL keeps whatever ?service_id= the user arrived with.
  const [selectionSettled, setSelectionSettled] = useState<boolean>(false);
  const [topologies, setTopologies] = useState<TopologyInfo[]>([]);
  const [deviations, setDeviations] = useState<Deviation[]>([]);
  const [anomalies, setAnomalies] = useState<Anomaly[]>([]);
  const [stats, setStats] = useState<Stats>({} as Stats);

  // Trace detail state
  const [selectedTrace, setSelectedTrace] = useState<string | null>(null);
  const [selectedTraceSource, setSelectedTraceSource] = useState<string>('');
  const [traceDetail, setTraceDetail] = useState<TraceDetail | null>(null);

  // Topology modal state
  const [showTopologyModal, setShowTopologyModal] = useState<boolean>(false);
  const [selectedTopology, setSelectedTopology] = useState<SelectedTopology | null>(null);

  // Filter state
  const [filterErrors, setFilterErrors] = useState<boolean>(false);
  const [filterDeviations, setFilterDeviations] = useState<boolean>(false);
  const [filterAnomalies, setFilterAnomalies] = useState<boolean>(false);
  const [serviceSearch, setServiceSearch] = useState<string>('');
  const [scope1Filter, setScope1Filter] = useState<string>('');
  const [scope2Filter, setScope2Filter] = useState<string>('');
  const [scope3Filter, setScope3Filter] = useState<string>('');
  const [envFilter, setEnvFilter] = useState<string>('');
  const [serviceIdentityFilter, setServiceIdentityFilter] = useState<string>('');

  // UI state
  const [autoRefresh, setAutoRefresh] = useState<boolean>(false);
  const [deviationLimit, setDeviationLimit] = useState<number>(20);
  const [anomalyLimit, setAnomalyLimit] = useState<number>(20);
  const [showBaselinePanel, setShowBaselinePanel] = useState<boolean>(true);

  // Variant state (combinations used for baseline selection UI)
  const [combinations, setCombinations] = useState<CombinationInfo[]>([]);
  const [activeCombination, setActiveCombination] = useState<string>('cumulative:50');

  // Computed filter options
  const scope1Options = Array.from(
    new Set(
      services
        .map(svc => svc.service_grouping?.scope1)
        .filter((v): v is string => typeof v === 'string' && v.length > 0)
    )
  ).sort();

  const scope2Options = Array.from(
    new Set(
      services
        .filter(svc => !scope1Filter || svc.service_grouping?.scope1 === scope1Filter)
        .map(svc => svc.service_grouping?.scope2)
        .filter((v): v is string => typeof v === 'string' && v.length > 0)
    )
  ).sort();

  const scope3Options = Array.from(
    new Set(
      services
        .filter(svc => !scope1Filter || svc.service_grouping?.scope1 === scope1Filter)
        .filter(svc => !scope2Filter || svc.service_grouping?.scope2 === scope2Filter)
        .map(svc => svc.service_grouping?.scope3)
        .filter((v): v is string => typeof v === 'string' && v.length > 0)
    )
  ).sort();

  const envOptions = Array.from(
    new Set(
      services
        .filter(svc => !scope1Filter || svc.service_grouping?.scope1 === scope1Filter)
        .filter(svc => !scope2Filter || svc.service_grouping?.scope2 === scope2Filter)
        .filter(svc => !scope3Filter || svc.service_grouping?.scope3 === scope3Filter)
        .map(svc => svc.service_grouping?.env)
        .filter((v): v is string => typeof v === 'string' && v.length > 0)
    )
  ).sort();

  // Filtered services list
  const filteredServices = services.filter(svc => {
    const scope1 = svc.service_grouping?.scope1 || '';
    const scope2 = svc.service_grouping?.scope2 || '';
    const scope3 = svc.service_grouping?.scope3 || '';
    const env = svc.service_grouping?.env || '';
    const serviceIdentity = svc.service_grouping?.service_identity || '';
    if (scope1Filter && scope1 !== scope1Filter) return false;
    if (scope2Filter && scope2 !== scope2Filter) return false;
    if (scope3Filter && scope3 !== scope3Filter) return false;
    if (envFilter && env !== envFilter) return false;
    if (serviceIdentityFilter && serviceIdentity !== serviceIdentityFilter) return false;
    if (filterErrors && !(svc.error_rate > 0)) return false;
    if (filterDeviations && !((svc.deviation_count || 0) > 0)) return false;
    if (filterAnomalies && !((svc.anomaly_count || 0) > 0)) return false;
    if (serviceSearch && !svc.service_id?.toLowerCase().includes(serviceSearch.toLowerCase())) return false;
    return true;
  });

  // Load UI state from localStorage
  useEffect(() => {
    try {
      const stored = localStorage.getItem(UI_STATE_KEY);
      if (!stored) return;
      const parsed = JSON.parse(stored);
      if (parsed?.selectedService) setSelectedService(parsed.selectedService);
      if (typeof parsed?.selectedTrace === 'string' && parsed.selectedTrace.length > 0) {
        setSelectedTrace(parsed.selectedTrace);
      }
      if (typeof parsed?.selectedTraceSource === 'string') {
        setSelectedTraceSource(parsed.selectedTraceSource);
      }
      if (typeof parsed?.filterErrors === 'boolean') setFilterErrors(parsed.filterErrors);
      if (typeof parsed?.filterDeviations === 'boolean') setFilterDeviations(parsed.filterDeviations);
      if (typeof parsed?.filterAnomalies === 'boolean') setFilterAnomalies(parsed.filterAnomalies);
      if (typeof parsed?.serviceSearch === 'string') setServiceSearch(parsed.serviceSearch);
      if (typeof parsed?.scope1Filter === 'string') setScope1Filter(parsed.scope1Filter);
      if (typeof parsed?.scope2Filter === 'string') setScope2Filter(parsed.scope2Filter);
      if (typeof parsed?.scope3Filter === 'string') setScope3Filter(parsed.scope3Filter);
      if (typeof parsed?.envFilter === 'string') setEnvFilter(parsed.envFilter);
      if (typeof parsed?.serviceIdentityFilter === 'string') setServiceIdentityFilter(parsed.serviceIdentityFilter);
      if (typeof parsed?.autoRefresh === 'boolean') {
        // Force auto-refresh off on load to prevent UI churn.
        setAutoRefresh(false);
      }
      if (typeof parsed?.deviationLimit === 'number') setDeviationLimit(parsed.deviationLimit);
      if (typeof parsed?.anomalyLimit === 'number') setAnomalyLimit(parsed.anomalyLimit);
    } catch (err) {
      console.error('Failed to load UI state', err);
    }
  }, []);

  // Applied by the first non-empty services load only, so later polls don't undo a user's pick.
  const initialUrlServiceRef = useRef<string | null>(null);

  useEffect(() => {
    initialUrlServiceRef.current = new URLSearchParams(window.location.search).get('service_id');
  }, []);

  // Save UI state to localStorage
  useEffect(() => {
    const payload = {
      selectedService,
      selectedTrace,
      selectedTraceSource,
      filterErrors,
      filterDeviations,
      filterAnomalies,
      serviceSearch,
      scope1Filter,
      scope2Filter,
      scope3Filter,
      envFilter,
      serviceIdentityFilter,
      autoRefresh,
      deviationLimit,
      anomalyLimit
    };
    try {
      localStorage.setItem(UI_STATE_KEY, JSON.stringify(payload));
    } catch (err) {
      console.error('Failed to save UI state', err);
    }
  }, [
    selectedService,
    selectedTrace,
    selectedTraceSource,
    filterErrors,
    filterDeviations,
    filterAnomalies,
    serviceSearch,
    scope1Filter,
    scope2Filter,
    scope3Filter,
    envFilter,
    serviceIdentityFilter,
    autoRefresh,
    deviationLimit,
    anomalyLimit
  ]);

  const servicesParams = new URLSearchParams(
    Object.entries({
      scope1: scope1Filter,
      scope2: scope2Filter,
      scope3: scope3Filter,
      env: envFilter,
      service_identity: serviceIdentityFilter,
    }).filter(([, value]) => value)
  ).toString();
  const servicesPath = `/api/services${servicesParams ? `?${servicesParams}` : ''}`;

  usePolling(async signal => {
    const data = (await getJSON<Service[] | null>(servicesPath, signal)) || [];
    setServices(data);
    const urlServiceId = initialUrlServiceRef.current;
    if (data.length > 0) {
      initialUrlServiceRef.current = null;
      setSelectionSettled(true);
    }
    setSelectedService(current => pickService(current, data, urlServiceId));
  }, autoRefresh ? 5000 : null, [servicesPath]);

  useEffect(() => {
    if (selectionSettled) syncServiceIdParam(selectedService);
  }, [selectionSettled, selectedService]);

  usePolling(async signal => {
    const data = await getJSON<CombinationsResponse>('/api/variants/combinations', signal);
    setCombinations(data.combinations || []);
    if (data.active) setActiveCombination(data.active);
  }, null, []);

  usePolling(async signal => {
    setStats(await getJSON<Stats>('/api/stats', signal));
  }, autoRefresh ? 2000 : null, []);

  const serviceDataEnabled = !!selectedService && !selectedTrace && !showTopologyModal;
  usePolling(async signal => {
    const serviceQuery = `service_id=${encodeURIComponent(selectedService!)}`;
    await Promise.all([
      getJSON<TopologyInfo[] | null>(`/api/topologies?${serviceQuery}`, signal).then(data => setTopologies(data || [])),
      getJSON<Deviation[] | null>(`/api/deviations?${serviceQuery}&limit=${deviationLimit}`, signal).then(data => setDeviations(data || [])),
      getJSON<Anomaly[] | null>(`/api/anomalies?${serviceQuery}&limit=${anomalyLimit}`, signal).then(data => setAnomalies(data || [])),
    ]);
  }, autoRefresh ? 3000 : null, [selectedService, deviationLimit, anomalyLimit], serviceDataEnabled);

  // Clear dependent filters when parent filter changes
  useEffect(() => {
    if (!scope1Filter) {
      if (scope2Filter) setScope2Filter('');
      if (scope3Filter) setScope3Filter('');
      if (envFilter) setEnvFilter('');
      if (serviceIdentityFilter) setServiceIdentityFilter('');
      return;
    }
    if (scope2Filter && !scope2Options.includes(scope2Filter)) {
      setScope2Filter('');
    }
    if (scope3Filter && !scope3Options.includes(scope3Filter)) {
      setScope3Filter('');
    }
    if (envFilter && !envOptions.includes(envFilter)) {
      setEnvFilter('');
    }
  }, [scope1Filter, scope2Filter, scope2Options, scope3Filter, scope3Options, envFilter, envOptions, serviceIdentityFilter]);

  // Fetch trace detail
  useEffect(() => {
    if (!selectedTrace) setTraceDetail(null);
  }, [selectedTrace]);

  usePolling(async signal => {
    setTraceDetail(await getJSON<TraceDetail>(`/api/traces/${encodeURIComponent(selectedTrace!)}`, signal));
  }, null, [selectedTrace], !!selectedTrace);

  // Handle topology click - fetch trace data and show modal
  const handleTopologyClick = async (topologyEvent: TopologyInfo | { payload?: TopologyInfo }) => {
    const topology = (topologyEvent as { payload?: TopologyInfo })?.payload || topologyEvent as TopologyInfo;
    if (!topology?.fingerprint) return;
    const serviceGrouping = services.find(s => s.service_id === selectedService)?.service_grouping || null;
    const serviceId = encodeURIComponent(selectedService!);
    const fingerprint = encodeURIComponent(topology.fingerprint);

    const showModal = (trace: TraceDetail, errorMessage: string | null) => {
      setSelectedTopology({ ...topology, trace, serviceGrouping, errorMessage });
      setShowTopologyModal(true);
    };
    const hasSpans = (trace: TraceDetail | null): trace is TraceDetail => !!trace?.spans && trace.spans.length > 0;
    const loadTrace = (traceId: string) =>
      orNullOnHttpError(getJSON<TraceDetail>(`/api/traces/${encodeURIComponent(traceId)}`));

    try {
      // Strategy 1: the topology example trace stored in the baseline
      const example = await orNullOnHttpError(
        getJSON<TraceDetail>(`/api/services/${serviceId}/topology-example?fingerprint=${fingerprint}`)
      );
      if (hasSpans(example)) return showModal(example, null);

      // Strategy 2: the dedicated by-fingerprint endpoint
      const byFingerprint = await orNullOnHttpError(
        getJSON<{ trace_id: string }[]>(`/api/traces/by-fingerprint?service_id=${serviceId}&fingerprint=${fingerprint}&limit=10`)
      );
      if (byFingerprint && byFingerprint.length > 0) {
        const trace = await loadTrace(byFingerprint[0].trace_id);
        if (hasSpans(trace)) return showModal(trace, null);
      }

      // Strategy 3: fall back to recent traces
      const recent = await orNullOnHttpError(
        getJSON<{ trace_id: string; fingerprint: string }[]>(`/api/traces/recent?service_id=${serviceId}&limit=200`)
      );
      const match = recent?.find(t => t.fingerprint === topology.fingerprint);
      if (match) {
        const trace = await loadTrace(match.trace_id);
        if (hasSpans(trace)) return showModal(trace, null);
      }

      showModal(EMPTY_TRACE, `No traces with span data found for this topology. This topology has ${topology.count || 0} occurrences but no stored trace details are available. Traces may have been cleaned up or this is a very rare topology.`);
    } catch (err) {
      console.error('Failed to load topology trace:', err);
      showModal(EMPTY_TRACE, `Failed to load trace data: ${(err as Error).message}. Please try again or check if traces are being stored.`);
    }
  };

  // Handle grouping chip click to set filter
  const handleGroupingChipClick = (key: string, value: string) => {
    if (!value) return;
    if (key === 'scope1') {
      setScope1Filter(value);
    } else if (key === 'scope2') {
      setScope2Filter(value);
    } else if (key === 'scope3') {
      setScope3Filter(value);
    } else if (key === 'env') {
      setEnvFilter(value);
    } else if (key === 'service_identity') {
      setServiceIdentityFilter(value);
    }
  };

  // Handle combination switch
  const handleCombinationSwitch = async (combinationName: string) => {
    try {
      const data = await postJSON<SetCombinationResponse>('/api/variants/combination', { combination: combinationName });
      setActiveCombination(data.combination);
      // Refresh services to show updated baselines
      setServices((await getJSON<Service[] | null>(servicesPath)) || []);
    } catch (err) {
      console.error('Failed to switch combination', err);
    }
  };

  // Handle service selection from sidebar
  const handleServiceSelect = (serviceId: string) => {
    setSelectedService(serviceId);
    setSelectedTrace(null);
  };

  // Handle filter changes from sidebar
  const handleFilterChange = (filterName: keyof SidebarFilters, value: string | boolean) => {
    switch (filterName) {
      case 'serviceSearch':
        setServiceSearch(value as string);
        break;
      case 'scope1Filter':
        setScope1Filter(value as string);
        break;
      case 'scope2Filter':
        setScope2Filter(value as string);
        break;
      case 'scope3Filter':
        setScope3Filter(value as string);
        break;
      case 'envFilter':
        setEnvFilter(value as string);
        break;
      case 'serviceIdentityFilter':
        setServiceIdentityFilter(value as string);
        break;
      case 'filterErrors':
        setFilterErrors(value as boolean);
        break;
      case 'filterDeviations':
        setFilterDeviations(value as boolean);
        break;
      case 'filterAnomalies':
        setFilterAnomalies(value as boolean);
        break;
    }
  };

  // Handle closing trace detail view
  const handleTraceDetailClose = () => {
    setSelectedTrace(null);
    setSelectedTraceSource('');
  };

  // Prepare sidebar props
  const sidebarFilters: SidebarFilters = {
    serviceSearch,
    scope1Filter,
    scope2Filter,
    scope3Filter,
    envFilter,
    serviceIdentityFilter,
    filterErrors,
    filterDeviations,
    filterAnomalies,
  };

  const sidebarFilterOptions: SidebarFilterOptions = {
    scope1Options,
    scope2Options,
    scope3Options,
    envOptions,
  };

  // Get current service data for display
  const currentService = services.find(s => s.service_id === selectedService);

  return (
    <div className="min-h-screen bg-slate-900 text-slate-100">
      <Header
        stats={stats}
        autoRefresh={autoRefresh}
        onAutoRefreshToggle={() => setAutoRefresh(prev => !prev)}
        combinations={combinations}
        activeCombination={activeCombination}
        onCombinationSwitch={handleCombinationSwitch}
        showBaselinePanel={showBaselinePanel}
        onToggleBaselinePanel={() => setShowBaselinePanel(prev => !prev)}
      />

      <div className="flex h-[calc(100vh-73px)]">
        <Sidebar
          services={filteredServices}
          selectedService={selectedService}
          onServiceSelect={handleServiceSelect}
          filters={sidebarFilters}
          onFilterChange={handleFilterChange}
          filterOptions={sidebarFilterOptions}
        />

        <main className="flex-1 p-6 overflow-y-auto h-full">
          {selectedTrace ? (
            traceDetail ? (
              <TraceDetailView
                trace={traceDetail}
                onClose={handleTraceDetailClose}
              />
            ) : (
              <div className="flex items-center justify-center h-full text-slate-500">
                Loading trace {selectedTrace.slice(0, 16)}...
              </div>
            )
          ) : selectedService ? (
            <div className="space-y-6">
              {/* Service header */}
              <div>
                <h2 className="text-xl font-semibold text-slate-100">{selectedService}</h2>
                <p className="text-slate-400 text-sm">
                  {currentService?.trace_count || 0} traces analyzed
                </p>
                {currentService?.service_grouping && (
                  <div className="mt-2">
                    <div className="text-[11px] uppercase text-slate-500 tracking-wide mb-1">
                      Service Identity Fields
                    </div>
                    <div className="flex flex-wrap gap-2 text-xs text-slate-400">
                      {Object.entries(currentService.service_grouping)
                        .filter(([, v]) => v)
                        .map(([k, v]) => {
                          const isFilterable = k === 'scope1' || k === 'scope2' || k === 'scope3' || k === 'env' || k === 'service_identity';
                          return (
                            <button
                              key={k}
                              onClick={() => handleGroupingChipClick(k, v as string)}
                              disabled={!isFilterable}
                              className={`border rounded px-2 py-0.5 ${
                                isFilterable
                                  ? 'bg-slate-800/60 border-slate-700/50 hover:bg-slate-700/50 cursor-pointer'
                                  : 'bg-slate-900/40 border-slate-800/50 cursor-default'
                              }`}
                              title={isFilterable ? `Filter by ${k}` : undefined}
                            >
                              <span className="text-slate-500">{k}=</span>{v}
                            </button>
                          );
                        })}
                    </div>
                  </div>
                )}
              </div>

              {/* Topology Distribution and Service Stats */}
              <div className="grid grid-cols-2 gap-6">
                <Card title="Topology Distribution">
                  {topologies.length > 0 ? (
                    <div className="flex items-center">
                      <ResponsiveContainer width="50%" height={200}>
                        <PieChart>
                          <Pie
                            data={topologies}
                            dataKey="percentage"
                            nameKey="fingerprint"
                            cx="50%"
                            cy="50%"
                            outerRadius={80}
                            onClick={(data) => handleTopologyClick(data)}
                            style={{ cursor: 'pointer' }}
                          >
                            {topologies.map((_, idx) => (
                              <Cell key={idx} fill={COLORS[idx % COLORS.length]} stroke="#374151" strokeWidth={2} />
                            ))}
                          </Pie>
                          <Tooltip formatter={(v: number) => `${v.toFixed(1)}%`} contentStyle={{ background: '#1e293b', border: 'none', borderRadius: '8px' }} />
                        </PieChart>
                      </ResponsiveContainer>
                      <div className="flex-1 space-y-2">
                        {topologies.slice(0, 5).map((t, idx) => (
                          <div
                            key={t.fingerprint}
                            className="flex items-center text-sm cursor-pointer hover:bg-slate-700/30 rounded p-1 transition-colors"
                            onClick={() => handleTopologyClick(t)}
                          >
                            <div className="w-3 h-3 rounded mr-2" style={{ backgroundColor: COLORS[idx % COLORS.length] }} />
                            <span className="truncate flex-1 font-mono text-xs text-slate-300">{t.fingerprint.slice(0, 12)}...</span>
                            <span className="text-slate-500 ml-2">{t.percentage.toFixed(1)}%</span>
                            {t.is_canonical && <span className="ml-2 text-xs bg-emerald-500/20 text-emerald-400 px-1.5 py-0.5 rounded">canonical</span>}
                          </div>
                        ))}
                      </div>
                    </div>
                  ) : (
                    <div className="text-slate-500 text-center py-10">No topology data yet</div>
                  )}
                </Card>

                <Card title="Service Stats">
                  {currentService && (() => {
                    const avgMs = (currentService.avg_duration_us || 0) / 1000;
                    const _p50Ms = (currentService.p50_duration_us || 0) / 1000; // Reserved for future p50 display
                    const p75Ms = (currentService.p75_duration_us || 0) / 1000;

                    // Calculate percentage change vs average
                    const p75VsAvg = avgMs > 0 ? ((p75Ms - avgMs) / avgMs * 100) : 0;
                    void _p50Ms; // Suppress unused variable warning

                    // Determine severity color based on how much p75 exceeds avg
                    const getLatencyVariant = (pct: number): 'success' | 'warning' | 'error' => {
                      if (pct < 20) return 'success';
                      if (pct < 100) return 'warning';
                      return 'error';
                    };

                    return (
                      <div className="grid grid-cols-2 gap-4">
                        <MetricCard
                          label="Avg Latency"
                          value={`${avgMs.toFixed(1)} ms`}
                        />
                        <MetricCard
                          label="P75 Latency"
                          value={`${p75Ms.toFixed(1)} ms`}
                          variant={getLatencyVariant(p75VsAvg)}
                          subtitle={p75VsAvg > 0 ? `+${p75VsAvg.toFixed(0)}% vs avg` : null}
                        />
                        <MetricCard
                          label="Error Rate"
                          value={`${(currentService.error_rate * 100).toFixed(2)}%`}
                          variant={currentService.error_rate > 0.01 ? 'error' : 'success'}
                        />
                        <MetricCard label="Unique Topologies" value={currentService.topology_count} />
                        <MetricCard label="Total Traces" value={currentService.trace_count} />
                      </div>
                    );
                  })()}
                </Card>
              </div>

              {/* Deviations and Anomalies */}
              <div className="grid grid-cols-2 gap-6">
                <Card
                  title={`Recent Deviations (${deviations.length})`}
                  helpText="Deviation = the execution path (topology) differs from the usual canonical path."
                >
                  <div className="space-y-2 max-h-64 overflow-y-auto">
                    {deviations.length > 0 ? deviations.map((d, idx) => (
                      <div
                        key={idx}
                        onClick={() => {
                          setSelectedTrace(d.trace_id);
                          setSelectedTraceSource('deviation');
                        }}
                        className={`rounded-lg p-3 text-sm cursor-pointer transition-colors border ${
                          selectedTrace === d.trace_id
                            ? 'bg-emerald-500/10 border-emerald-500/40'
                            : 'bg-slate-700/30 hover:bg-slate-700/50 border-slate-600/30'
                        }`}
                      >
                        <div className="flex justify-between items-start">
                          <div className="flex-1 min-w-0">
                            <div className="font-mono text-xs text-slate-300 truncate">
                              Fingerprint: {d.fingerprint?.slice(0, 16)}...
                            </div>
                            <div className="font-mono text-[10px] text-slate-500 truncate mt-0.5">
                              Trace: {d.trace_id?.slice(0, 16)}...
                            </div>
                          </div>
                          <span className={`px-2 py-0.5 rounded text-xs ml-2 flex-shrink-0 ${
                            d.score > 0.5 ? 'bg-red-500/20 text-red-400' :
                            d.score > 0.3 ? 'bg-amber-500/20 text-amber-400' : 'bg-emerald-500/20 text-emerald-400'
                          }`}>
                            {(d.score * 100).toFixed(0)}% deviation
                          </span>
                        </div>
                        <p className="text-slate-500 text-xs mt-1 truncate">
                          Reason: {d.diff_summary || 'Execution path differs from the usual (canonical) topology'}
                        </p>
                      </div>
                    )) : (
                      <div className="text-slate-500 text-center py-6">No deviations detected</div>
                    )}
                  </div>
                  <div className="mt-2 flex justify-end">
                    <button
                      onClick={() => setDeviationLimit(prev => prev + 20)}
                      className="text-xs px-2 py-1 rounded border border-slate-600/40 text-slate-300 hover:border-slate-500/60"
                    >
                      Show more
                    </button>
                  </div>
                </Card>

                <Card
                  title={`Recent Anomalies (${anomalies.length})`}
                  helpText={`Anomaly = latency is unusually high vs baseline (Z-score on p${Math.round((window.APP_CONFIG?.ANOMALY_BASELINE_PERCENTILE || 0.75) * 100)}).`}
                >
                  <div className="space-y-2 max-h-64 overflow-y-auto">
                    {anomalies.length > 0 ? anomalies.map((a, idx) => (
                      <div
                        key={idx}
                        onClick={() => {
                          setSelectedTrace(a.trace_id);
                          setSelectedTraceSource('anomaly');
                        }}
                        className={`rounded-lg p-3 text-sm cursor-pointer transition-colors border ${
                          selectedTrace === a.trace_id
                            ? 'bg-emerald-500/10 border-emerald-500/40'
                            : 'bg-slate-700/30 hover:bg-slate-700/50 border-slate-600/30'
                        }`}
                      >
                        <div className="flex justify-between items-start">
                          <span className="font-mono text-xs text-slate-300">{a.trace_id?.slice(0, 16)}...</span>
                          <span className="bg-red-500/20 text-red-400 px-2 py-0.5 rounded text-xs">
                            Z-score (latency): {a.score?.toFixed(1)}
                          </span>
                        </div>
                        <div className="text-slate-500 text-xs mt-1">
                          Reason: {a.slow_branches?.length > 0
                            ? `Slow branches: ${a.slow_branches.slice(0, 2).join(', ')}`
                            : 'Latency outlier (modified Z-score threshold 3.5)'}
                        </div>
                      </div>
                    )) : (
                      <div className="text-slate-500 text-center py-6">No anomalies detected</div>
                    )}
                  </div>
                  <div className="mt-2 flex justify-end">
                    <button
                      onClick={() => setAnomalyLimit(prev => prev + 20)}
                      className="text-xs px-2 py-1 rounded border border-slate-600/40 text-slate-300 hover:border-slate-500/60"
                    >
                      Show more
                    </button>
                  </div>
                </Card>
              </div>
            </div>
          ) : (
            <div className="flex items-center justify-center h-full text-slate-500">Select a service to view details</div>
          )}
        </main>
      </div>

      {showTopologyModal && selectedTopology && (
        <TopologyModal
          topology={{
            fingerprint: selectedTopology.fingerprint,
            count: selectedTopology.count,
            percentage: selectedTopology.percentage,
            serviceGrouping: selectedTopology.serviceGrouping as Record<string, string | null | undefined> | null | undefined,
            errorMessage: selectedTopology.errorMessage,
          }}
          onClose={() => setShowTopologyModal(false)}
          graphData={buildGraphData(selectedTopology.trace)}
        />
      )}
    </div>
  );
}

export default App;
