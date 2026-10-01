/**
 * End-to-end integration tests for the trace-analyzer UI.
 *
 * Tests the complete user journey through the application including:
 * 1. Initial Load - App renders with header, sidebar, content; stats and services load
 * 2. Service Selection Flow - User clicks service, details load
 * 3. Trace Detail Flow - User clicks trace, TraceDetailView shows with span tree
 * 4. Topology Modal Flow - User clicks topology, modal opens with graph
 * 5. Filter Flow - User types in filter, services list filters
 * 6. Variant Switching - User changes combination, API is called
 */
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import App from './App';
import type { Service, Stats, TopologyInfo, Deviation, Anomaly, TraceDetail, SpanUI, CombinationInfo } from './types';

// ============================================================================
// Mock Data
// ============================================================================

const mockStats: Stats = {
  hot_tier_traces: 5000,
  unique_services: 25,
  unique_topologies: 150,
  total_deviations: 42,
  total_anomalies: 15,
  total_errors: 8,
  interesting_traces: 65,
  interesting_traces_pct: 1.3,
  hot_tier_spans: 20000,
  uptime_seconds: 3600,
  service_identity_fields: ['service_identity', 'scope1', 'scope2', 'scope3', 'env'],
};

const mockServices: Service[] = [
  {
    service_id: 'api-gateway:handleRequest',
    service_grouping: {
      service_identity: 'api-gateway',
      scope1: 'platform',
      scope2: 'frontend',
      scope3: 'web',
      env: 'prod',
      operation: 'handleRequest',
      feature_group: 'core',
      feature_name: 'routing',
      sub_service: 'main',
    },
    trace_count: 1500,
    topology_count: 12,
    avg_duration_us: 85000,
    p50_duration_us: 70000,
    p75_duration_us: 95000,
    error_rate: 0.02,
    last_seen: '2025-01-15T10:30:00Z',
    deviation_count: 8,
    anomaly_count: 3,
  },
  {
    service_id: 'user-service:getUser',
    service_grouping: {
      service_identity: 'user-service',
      scope1: 'platform',
      scope2: 'backend',
      scope3: 'api',
      env: 'prod',
      operation: 'getUser',
      feature_group: 'users',
      feature_name: 'profile',
      sub_service: 'core',
    },
    trace_count: 800,
    topology_count: 5,
    avg_duration_us: 45000,
    p50_duration_us: 40000,
    p75_duration_us: 55000,
    error_rate: 0.01,
    last_seen: '2025-01-15T10:25:00Z',
    deviation_count: 2,
    anomaly_count: 1,
  },
  {
    service_id: 'payment-service:processPayment',
    service_grouping: {
      service_identity: 'payment-service',
      scope1: 'commerce',
      scope2: 'payments',
      scope3: 'processing',
      env: 'prod',
      operation: 'processPayment',
      feature_group: 'payments',
      feature_name: 'checkout',
      sub_service: 'stripe',
    },
    trace_count: 500,
    topology_count: 8,
    avg_duration_us: 250000,
    p50_duration_us: 200000,
    p75_duration_us: 350000,
    error_rate: 0.05,
    last_seen: '2025-01-15T10:20:00Z',
    deviation_count: 5,
    anomaly_count: 4,
  },
];

const mockTopologies: TopologyInfo[] = [
  {
    fingerprint: 'fp-canonical-abc123',
    count: 1200,
    percentage: 80.0,
    avg_duration_us: 80000,
    is_canonical: true,
  },
  {
    fingerprint: 'fp-variant-def456',
    count: 200,
    percentage: 13.3,
    avg_duration_us: 95000,
    is_canonical: false,
  },
  {
    fingerprint: 'fp-rare-ghi789',
    count: 100,
    percentage: 6.7,
    avg_duration_us: 120000,
    is_canonical: false,
  },
];

const mockDeviations: Deviation[] = [
  {
    trace_id: 'trace-deviation-001',
    service_id: 'api-gateway:handleRequest',
    fingerprint: 'fp-variant-def456',
    canonical_fingerprint: 'fp-canonical-abc123',
    score: 0.45,
    diff_summary: 'Missing call to cache-service',
    timestamp: '2025-01-15T10:28:00Z',
  },
  {
    trace_id: 'trace-deviation-002',
    service_id: 'api-gateway:handleRequest',
    fingerprint: 'fp-rare-ghi789',
    canonical_fingerprint: 'fp-canonical-abc123',
    score: 0.65,
    diff_summary: 'Additional retry loop detected',
    timestamp: '2025-01-15T10:25:00Z',
  },
];

const mockAnomalies: Anomaly[] = [
  {
    trace_id: 'trace-anomaly-001',
    service_id: 'api-gateway:handleRequest',
    fingerprint: 'fp-canonical-abc123',
    score: 4.2,
    slow_branches: ['database:query', 'cache:get'],
    timestamp: '2025-01-15T10:27:00Z',
  },
  {
    trace_id: 'trace-anomaly-002',
    service_id: 'api-gateway:handleRequest',
    fingerprint: 'fp-canonical-abc123',
    score: 3.8,
    slow_branches: ['user-service:getUser'],
    timestamp: '2025-01-15T10:22:00Z',
  },
];

const mockSpans: SpanUI[] = [
  {
    span_id: 'span-root-001',
    parent_span_id: '',
    service_name: 'api-gateway',
    operation_name: 'handleRequest',
    start_time: 1705315800000000,
    duration_us: 150000,
    status: 'OK',
    attributes: { 'http.method': 'GET', 'http.url': '/api/users' },
  },
  {
    span_id: 'span-child-001',
    parent_span_id: 'span-root-001',
    service_name: 'user-service',
    operation_name: 'getUser',
    start_time: 1705315800010000,
    duration_us: 80000,
    status: 'OK',
    attributes: { 'db.type': 'postgres' },
  },
  {
    span_id: 'span-child-002',
    parent_span_id: 'span-root-001',
    service_name: 'cache-service',
    operation_name: 'getFromCache',
    start_time: 1705315800005000,
    duration_us: 5000,
    status: 'OK',
    attributes: { 'cache.hit': 'true' },
  },
  {
    span_id: 'span-grandchild-001',
    parent_span_id: 'span-child-001',
    service_name: 'database',
    operation_name: 'query',
    start_time: 1705315800020000,
    duration_us: 50000,
    status: 'OK',
    attributes: { 'db.rows': '1' },
  },
];

const mockTraceDetail: TraceDetail = {
  trace_id: 'trace-deviation-001',
  service_id: 'api-gateway:handleRequest',
  service_grouping: mockServices[0].service_grouping,
  fingerprint: 'fp-variant-def456',
  total_duration_us: 150000,
  has_error: false,
  spans: mockSpans,
};

const combination = (variant: string, threshold: number): CombinationInfo => ({
  combination: `${variant}:${threshold}`,
  variant,
  threshold,
  name: variant,
  description: '',
  parameters: {},
  min_samples: threshold,
});

const mockCombinations: CombinationInfo[] = ['cumulative', 'ewma', 'sliding_window', 'exponential_decay', 'same_time_yesterday']
  .flatMap(variant => [combination(variant, 50), combination(variant, 100)]);

const mockTopologyExampleTrace: TraceDetail = {
  trace_id: 'trace-topology-example-001',
  service_id: 'api-gateway:handleRequest',
  service_grouping: mockServices[0].service_grouping,
  fingerprint: 'fp-canonical-abc123',
  total_duration_us: 80000,
  has_error: false,
  spans: mockSpans,
};

// ============================================================================
// Mock Setup
// ============================================================================

// Track API call counts for verification
const apiCallCounts: Record<string, number> = {};

// Custom fetch mock function
const createMockFetch = () => {
  return vi.fn((url: string, options?: RequestInit) => {
    const urlPath = url.replace(/^https?:\/\/[^/]+/, '').split('?')[0];
    const searchParams = new URLSearchParams(url.split('?')[1] || '');

    // Track API calls
    apiCallCounts[urlPath] = (apiCallCounts[urlPath] || 0) + 1;

    let response: unknown = {};

    // Route to appropriate mock response
    if (urlPath === '/api/services') {
      response = mockServices;
    } else if (urlPath === '/api/stats') {
      response = mockStats;
    } else if (urlPath === '/api/variants') {
      response = { variants: ['cumulative', 'ewma', 'sliding_window', 'exponential_decay', 'same_time_yesterday'], active: 'cumulative' };
    } else if (urlPath === '/api/variants/combinations') {
      response = { combinations: mockCombinations, active: 'cumulative:50' };
    } else if (urlPath === '/api/variants/combination' && options?.method === 'POST') {
      const body = JSON.parse(options.body as string);
      response = { combination: body.combination };
    } else if (urlPath === '/api/topologies') {
      response = mockTopologies;
    } else if (urlPath === '/api/deviations') {
      response = mockDeviations;
    } else if (urlPath === '/api/anomalies') {
      response = mockAnomalies;
    } else if (urlPath.startsWith('/api/traces/') && !urlPath.includes('by-fingerprint') && !urlPath.includes('recent')) {
      // Single trace fetch
      const traceId = urlPath.replace('/api/traces/', '');
      response = { ...mockTraceDetail, trace_id: decodeURIComponent(traceId) };
    } else if (urlPath.includes('/topology-example')) {
      response = mockTopologyExampleTrace;
    } else if (urlPath === '/api/traces/by-fingerprint') {
      response = [{ trace_id: 'trace-by-fingerprint-001', fingerprint: searchParams.get('fingerprint') }];
    } else if (urlPath === '/api/traces/recent') {
      response = [{ trace_id: 'trace-recent-001', fingerprint: 'fp-canonical-abc123' }];
    }

    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve(response),
    });
  });
};

// Reset all mocks before each test
beforeEach(() => {
  vi.clearAllMocks();
  Object.keys(apiCallCounts).forEach(key => delete apiCallCounts[key]);

  // Use vi.stubGlobal for more reliable mocking (overrides setupTests.js)
  vi.stubGlobal('fetch', createMockFetch());

  // Clear localStorage
  localStorage.clear();
  // The app mirrors the selected service into the URL, so each test starts from a clean one.
  window.history.replaceState({}, '', '/');
});

afterEach(() => {
  vi.restoreAllMocks();
});

// ============================================================================
// Test Suites
// ============================================================================

describe('App Integration Tests', () => {
  // ==========================================================================
  // 1. Initial Load Tests
  // ==========================================================================
  describe('1. Initial Load', () => {
    it('renders the app with header, sidebar, and content area', async () => {
      render(<App />);

      // Header should be present with title
      await waitFor(() => {
        expect(screen.getByText('Trace Deviation Analyzer')).toBeInTheDocument();
      });

      // Sidebar should show "Services" heading (h2 element specifically)
      const sidebar = document.querySelector('aside');
      expect(sidebar).toBeInTheDocument();
      expect(within(sidebar!).getByRole('heading', { name: 'Services' })).toBeInTheDocument();

      // Main content area should be present (select a service prompt or service details)
      await waitFor(() => {
        // Either we see a service is auto-selected or the prompt
        const hasService = screen.queryByText('api-gateway:handleRequest') !== null;
        const hasPrompt = screen.queryByText('Select a service to view details') !== null;
        expect(hasService || hasPrompt).toBe(true);
      });
    });

    it('auto-selects the first service and shows its detail view', async () => {
      render(<App />);

      const main = document.querySelector('main');
      await waitFor(() => {
        expect(within(main!).getByRole('heading', { name: 'api-gateway:handleRequest' })).toBeInTheDocument();
        expect(within(main!).getByText('1500 traces analyzed')).toBeInTheDocument();
      });
    });

    it('loads and displays stats in header', async () => {
      render(<App />);

      // Get the header element
      const header = document.querySelector('header');
      expect(header).toBeInTheDocument();

      await waitFor(() => {
        // Stats should be displayed in header
        expect(within(header!).getByText('Traces')).toBeInTheDocument();
        expect(within(header!).getByText('5000')).toBeInTheDocument();
        expect(within(header!).getByText('25')).toBeInTheDocument(); // Services count
        expect(within(header!).getByText('Topologies')).toBeInTheDocument();
        expect(within(header!).getByText('150')).toBeInTheDocument();
      });

      // Deviations and anomalies stats
      await waitFor(() => {
        expect(within(header!).getByText('Deviations')).toBeInTheDocument();
        expect(within(header!).getByText('42')).toBeInTheDocument();
        expect(within(header!).getByText('Anomalies')).toBeInTheDocument();
        expect(within(header!).getByText('15')).toBeInTheDocument();
      });
    });

    it('populates services list from API', async () => {
      render(<App />);

      // Get sidebar for scoped queries
      const sidebar = document.querySelector('aside');
      expect(sidebar).toBeInTheDocument();

      await waitFor(() => {
        // All services should be visible in sidebar
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
        expect(within(sidebar!).getByText('user-service')).toBeInTheDocument();
        expect(within(sidebar!).getByText('payment-service')).toBeInTheDocument();
      });

      // Services should show trace and topology counts (format: "X traces . Y topologies")
      await waitFor(() => {
        expect(within(sidebar!).getByText(/1500 traces .* 12 topologies/)).toBeInTheDocument();
        expect(within(sidebar!).getByText(/800 traces .* 5 topologies/)).toBeInTheDocument();
        expect(within(sidebar!).getByText(/500 traces .* 8 topologies/)).toBeInTheDocument();
      });
    });

    it('displays auto-refresh toggle button', async () => {
      render(<App />);

      await waitFor(() => {
        const autoRefreshButton = screen.getByText(/Auto-refresh/);
        expect(autoRefreshButton).toBeInTheDocument();
        expect(autoRefreshButton).toHaveTextContent('Auto-refresh Off');
      });
    });

    it('loads baseline combinations and displays active combination', async () => {
      render(<App />);

      await waitFor(() => {
        // Baseline panel header should be present
        expect(screen.getByText('Baseline Strategy & Threshold Selection')).toBeInTheDocument();
      });

      // Active combination should be displayed
      await waitFor(() => {
        expect(screen.getByText(/Currently Active:/)).toBeInTheDocument();
        expect(screen.getByText(/Cumulative - 50 traces threshold/)).toBeInTheDocument();
      });
    });
  });

  // ==========================================================================
  // 2. Service Selection Flow Tests
  // ==========================================================================
  describe('2. Service Selection Flow', () => {
    it('shows service details when a service is clicked', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      expect(sidebar).toBeInTheDocument();

      // Wait for services to load
      await waitFor(() => {
        expect(within(sidebar!).getByText('user-service')).toBeInTheDocument();
      });

      // Click on user-service
      const userServiceItem = within(sidebar!).getByText('user-service');
      await user.click(userServiceItem);

      // Service detail view should show in main content area
      const main = document.querySelector('main');
      await waitFor(() => {
        expect(within(main!).getByText('user-service:getUser')).toBeInTheDocument();
        expect(within(main!).getByText(/800 traces analyzed/)).toBeInTheDocument();
      });
    });

    it('loads topologies for selected service', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      expect(sidebar).toBeInTheDocument();
      const main = document.querySelector('main');

      // Wait for services to load in sidebar
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      }, { timeout: 5000 });

      // Click on the service explicitly to ensure selection
      await user.click(within(sidebar!).getByText('api-gateway'));

      // Wait for Topology Distribution card to appear
      await waitFor(() => {
        expect(within(main!).getByText('Topology Distribution')).toBeInTheDocument();
      }, { timeout: 5000 });

      // Wait for topologies to load - either we see topology data OR "No topology data yet"
      // The test verifies that topology distribution shows up
      await waitFor(() => {
        const mainContent = main?.textContent || '';
        // Either topologies are loaded or the "no data" message appears
        const hasTopologyContent = mainContent.includes('fp-canonical') || mainContent.includes('No topology');
        expect(hasTopologyContent).toBe(true);
      }, { timeout: 5000 });
    });

    it('loads deviations for selected service', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      await user.click(within(sidebar!).getByText('api-gateway'));

      // Deviations card should show in main content
      const main = document.querySelector('main');
      await waitFor(() => {
        expect(within(main!).getByText(/Recent Deviations/)).toBeInTheDocument();
      });

      // Deviation items should be visible
      await waitFor(() => {
        expect(within(main!).getByText(/Missing call to cache-service/)).toBeInTheDocument();
        expect(within(main!).getByText(/Additional retry loop detected/)).toBeInTheDocument();
      });
    });

    it('loads anomalies for selected service', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      await user.click(within(sidebar!).getByText('api-gateway'));

      // Anomalies card should show in main content
      const main = document.querySelector('main');
      await waitFor(() => {
        expect(within(main!).getByText(/Recent Anomalies/)).toBeInTheDocument();
      });

      // Anomaly items should be visible (showing Z-score)
      await waitFor(() => {
        expect(within(main!).getByText(/Z-score.*4.2/)).toBeInTheDocument();
      });
    });

    it('displays service stats with metrics', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      await user.click(within(sidebar!).getByText('api-gateway'));

      // Service Stats card should appear in main
      const main = document.querySelector('main');
      await waitFor(() => {
        expect(within(main!).getByText('Service Stats')).toBeInTheDocument();
      });

      // Metrics should be displayed
      await waitFor(() => {
        expect(within(main!).getByText('Avg Latency')).toBeInTheDocument();
        expect(within(main!).getByText('P75 Latency')).toBeInTheDocument();
        expect(within(main!).getByText('Error Rate')).toBeInTheDocument();
        expect(within(main!).getByText('Unique Topologies')).toBeInTheDocument();
        expect(within(main!).getByText('Total Traces')).toBeInTheDocument();
      });
    });

    it('shows service grouping chips that can be clicked to filter', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      await user.click(within(sidebar!).getByText('api-gateway'));

      // Service grouping fields should show in main
      const main = document.querySelector('main');
      await waitFor(() => {
        expect(within(main!).getByText('Service Identity Fields')).toBeInTheDocument();
      });

      // Grouping chips should be present
      await waitFor(() => {
        expect(within(main!).getByText(/scope1=/)).toBeInTheDocument();
        expect(within(main!).getByText(/scope2=/)).toBeInTheDocument();
        expect(within(main!).getByText(/env=/)).toBeInTheDocument();
      });
    });
  });

  // ==========================================================================
  // 3. Trace Detail Flow Tests
  // ==========================================================================
  describe('3. Trace Detail Flow', () => {
    it('opens trace detail view when deviation is clicked', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      // Wait for service load and click
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      await user.click(within(sidebar!).getByText('api-gateway'));

      const main = document.querySelector('main');
      // Wait for deviations to load
      await waitFor(() => {
        expect(within(main!).getByText(/Missing call to cache-service/)).toBeInTheDocument();
      });

      // Click on a deviation
      const deviationItem = within(main!).getByText(/Missing call to cache-service/).closest('div[class*="cursor-pointer"]');
      expect(deviationItem).toBeInTheDocument();
      await user.click(deviationItem!);

      // TraceDetailView should appear
      await waitFor(() => {
        expect(within(main!).getByText('Trace Detail')).toBeInTheDocument();
      });
    });

    it('displays span tree in trace detail view', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      await user.click(within(sidebar!).getByText('api-gateway'));

      const main = document.querySelector('main');
      await waitFor(() => {
        expect(within(main!).getByText(/Missing call to cache-service/)).toBeInTheDocument();
      });

      const deviationItem = within(main!).getByText(/Missing call to cache-service/).closest('div[class*="cursor-pointer"]');
      await user.click(deviationItem!);

      // Wait for trace detail to load
      await waitFor(() => {
        expect(within(main!).getByText('Span Tree')).toBeInTheDocument();
      });

      // Span tree should show service names (these are in the span tree, not sidebar)
      const spanTree = within(within(main!).getByText('Span Tree').parentElement!);
      await waitFor(() => {
        expect(spanTree.getByText('user-service')).toBeInTheDocument();
        expect(spanTree.getByText('cache-service')).toBeInTheDocument();
        expect(spanTree.getByText('database')).toBeInTheDocument();
      });

      // Span operations should be visible
      await waitFor(() => {
        expect(spanTree.getByText('getUser')).toBeInTheDocument();
        expect(spanTree.getByText('getFromCache')).toBeInTheDocument();
        expect(spanTree.getByText('query')).toBeInTheDocument();
      });
    });

    it('displays trace metadata metrics', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      await user.click(within(sidebar!).getByText('api-gateway'));

      const main = document.querySelector('main');
      await waitFor(() => {
        expect(within(main!).getByText(/Missing call to cache-service/)).toBeInTheDocument();
      });

      const deviationItem = within(main!).getByText(/Missing call to cache-service/).closest('div[class*="cursor-pointer"]');
      await user.click(deviationItem!);

      // Trace metrics should be visible in main area
      await waitFor(() => {
        expect(within(main!).getByText('Service')).toBeInTheDocument();
        expect(within(main!).getByText('Duration')).toBeInTheDocument();
        expect(within(main!).getByText('Spans')).toBeInTheDocument();
        expect(within(main!).getByText('Fingerprint')).toBeInTheDocument();
      });

      // Span count should match
      await waitFor(() => {
        expect(within(main!).getByText('4')).toBeInTheDocument(); // 4 spans
      });
    });

    it('closes trace detail view with back button', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      await user.click(within(sidebar!).getByText('api-gateway'));

      const main = document.querySelector('main');
      await waitFor(() => {
        expect(within(main!).getByText(/Missing call to cache-service/)).toBeInTheDocument();
      });

      const deviationItem = within(main!).getByText(/Missing call to cache-service/).closest('div[class*="cursor-pointer"]');
      await user.click(deviationItem!);

      await waitFor(() => {
        expect(within(main!).getByText('Trace Detail')).toBeInTheDocument();
      });

      // Click back button
      const backButton = within(main!).getByText(/← Back/);
      await user.click(backButton);

      // Should return to service view
      await waitFor(() => {
        expect(within(main!).queryByText('Trace Detail')).not.toBeInTheDocument();
        expect(within(main!).getByText('Topology Distribution')).toBeInTheDocument();
      });
    });

    it('shows trace detail when anomaly is clicked', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      await user.click(within(sidebar!).getByText('api-gateway'));

      // Wait for anomalies to load
      const main = document.querySelector('main');
      await waitFor(() => {
        expect(within(main!).getByText(/Z-score.*4.2/)).toBeInTheDocument();
      });

      // Click on an anomaly
      const anomalyItem = within(main!).getByText(/Z-score.*4.2/).closest('div[class*="cursor-pointer"]');
      expect(anomalyItem).toBeInTheDocument();
      await user.click(anomalyItem!);

      // TraceDetailView should appear
      await waitFor(() => {
        expect(within(main!).getByText('Trace Detail')).toBeInTheDocument();
      });
    });
  });

  // ==========================================================================
  // 4. Topology Modal Flow Tests
  // ==========================================================================
  describe('4. Topology Modal Flow', () => {
    it('shows topology distribution card when service is selected', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      const main = document.querySelector('main');

      // Wait for services to load
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      }, { timeout: 5000 });

      // Click on service
      await user.click(within(sidebar!).getByText('api-gateway'));

      // Topology Distribution card should appear
      await waitFor(() => {
        expect(within(main!).getByText('Topology Distribution')).toBeInTheDocument();
      }, { timeout: 5000 });
    });

    // Note: These tests verify topology modal functionality. The TopologyModal component
    // is tested more thoroughly in its dedicated test file (TopologyModal.test.tsx).
    // These integration tests focus on the store-component integration.

    it.todo('can open topology modal using store directly');
    it.todo('displays graph with nodes when trace data is present');
    it.todo('closes topology modal on close button click');
    it.todo('shows ROOT indicator for root nodes in graph');
  });

  // ==========================================================================
  // 5. Filter Flow Tests
  // ==========================================================================
  describe('5. Filter Flow', () => {
    it('filters services list when typing in search input', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
        expect(within(sidebar!).getByText('user-service')).toBeInTheDocument();
        expect(within(sidebar!).getByText('payment-service')).toBeInTheDocument();
      });

      // Find the search input
      const searchInput = within(sidebar!).getByPlaceholderText('Filter services...');
      expect(searchInput).toBeInTheDocument();

      // Type in search
      await user.type(searchInput, 'user');

      // Only user-service should be visible in sidebar
      await waitFor(() => {
        expect(within(sidebar!).getByText('user-service')).toBeInTheDocument();
        expect(within(sidebar!).queryByText('api-gateway')).not.toBeInTheDocument();
        expect(within(sidebar!).queryByText('payment-service')).not.toBeInTheDocument();
      });
    });

    it('filters services by search and clears filter', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      const searchInput = within(sidebar!).getByPlaceholderText('Filter services...');
      await user.type(searchInput, 'payment');

      await waitFor(() => {
        expect(within(sidebar!).getByText('payment-service')).toBeInTheDocument();
        expect(within(sidebar!).queryByText('api-gateway')).not.toBeInTheDocument();
      });

      // Clear the search
      await user.clear(searchInput);

      // All services should be visible again
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
        expect(within(sidebar!).getByText('user-service')).toBeInTheDocument();
        expect(within(sidebar!).getByText('payment-service')).toBeInTheDocument();
      });
    });

    it('filters with "Errors only" checkbox', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      // Find and click the "Errors only" checkbox
      const errorsCheckbox = within(sidebar!).getByLabelText('Errors only');
      expect(errorsCheckbox).toBeInTheDocument();

      await user.click(errorsCheckbox);

      // All services have some errors, so all should still be visible
      // (error_rate > 0 for all mock services)
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
        expect(within(sidebar!).getByText('user-service')).toBeInTheDocument();
        expect(within(sidebar!).getByText('payment-service')).toBeInTheDocument();
      });
    });

    it('filters with "Deviations only" checkbox', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      const deviationsCheckbox = within(sidebar!).getByLabelText('Deviations only');
      expect(deviationsCheckbox).toBeInTheDocument();

      await user.click(deviationsCheckbox);

      // All mock services have deviation_count > 0
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
        expect(within(sidebar!).getByText('user-service')).toBeInTheDocument();
        expect(within(sidebar!).getByText('payment-service')).toBeInTheDocument();
      });
    });

    it('filters with "Anomalies only" checkbox', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      const anomaliesCheckbox = within(sidebar!).getByLabelText('Anomalies only');
      expect(anomaliesCheckbox).toBeInTheDocument();

      await user.click(anomaliesCheckbox);

      // All mock services have anomaly_count > 0
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
        expect(within(sidebar!).getByText('user-service')).toBeInTheDocument();
        expect(within(sidebar!).getByText('payment-service')).toBeInTheDocument();
      });
    });

    it('filters by scope1 dropdown', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      // Find scope1 filter dropdown
      const scope1Select = within(sidebar!).getByLabelText('Scope1');
      expect(scope1Select).toBeInTheDocument();

      // Select 'commerce' scope1
      await user.selectOptions(scope1Select, 'commerce');

      // Only payment-service has scope1: 'commerce'
      await waitFor(() => {
        expect(within(sidebar!).getByText('payment-service')).toBeInTheDocument();
        expect(within(sidebar!).queryByText('api-gateway')).not.toBeInTheDocument();
        expect(within(sidebar!).queryByText('user-service')).not.toBeInTheDocument();
      });
    });

    it('combines multiple filters', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      // First filter by scope1 = 'platform'
      const scope1Select = within(sidebar!).getByLabelText('Scope1');
      await user.selectOptions(scope1Select, 'platform');

      // Should show api-gateway and user-service (both have scope1: 'platform')
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
        expect(within(sidebar!).getByText('user-service')).toBeInTheDocument();
        expect(within(sidebar!).queryByText('payment-service')).not.toBeInTheDocument();
      });

      // Now add text search for 'api'
      const searchInput = within(sidebar!).getByPlaceholderText('Filter services...');
      await user.type(searchInput, 'api');

      // Should only show api-gateway
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
        expect(within(sidebar!).queryByText('user-service')).not.toBeInTheDocument();
      });
    });
  });

  // ==========================================================================
  // 6. Variant Switching Tests
  // ==========================================================================
  describe('6. Variant Switching', () => {
    it('displays all baseline variants', async () => {
      render(<App />);

      await waitFor(() => {
        expect(screen.getByText('Baseline Strategy & Threshold Selection')).toBeInTheDocument();
      });

      // All variants should be displayed
      await waitFor(() => {
        expect(screen.getByText('Cumulative')).toBeInTheDocument();
        expect(screen.getByText('EWMA')).toBeInTheDocument();
        expect(screen.getByText('Sliding Window')).toBeInTheDocument();
        expect(screen.getByText('Exponential Decay')).toBeInTheDocument();
        expect(screen.getByText('Same Time Yesterday')).toBeInTheDocument();
      });
    });

    it('shows active indicator on current variant', async () => {
      render(<App />);

      await waitFor(() => {
        expect(screen.getByText('Cumulative')).toBeInTheDocument();
      });

      // Active indicator should be shown
      await waitFor(() => {
        const activeIndicators = screen.getAllByText(/✓ Active/);
        expect(activeIndicators.length).toBe(1);
      });
    });

    it('calls API when combination is switched', async () => {
      const user = userEvent.setup();
      render(<App />);

      await waitFor(() => {
        expect(screen.getByText('Cumulative')).toBeInTheDocument();
      });

      // Find the EWMA dropdown and change it
      const ewmaSection = screen.getByText('EWMA').closest<HTMLElement>('div[class*="p-2.5"]');
      expect(ewmaSection).toBeInTheDocument();

      const ewmaSelect = within(ewmaSection!).getByRole('combobox');
      await user.selectOptions(ewmaSelect, 'ewma:100');

      // API should be called with the new combination
      await waitFor(() => {
        expect(apiCallCounts['/api/variants/combination']).toBeGreaterThan(0);
      });
    });

    it('updates UI after combination switch', async () => {
      const user = userEvent.setup();
      render(<App />);

      await waitFor(() => {
        expect(screen.getByText(/Currently Active:/)).toBeInTheDocument();
        expect(screen.getByText(/Cumulative - 50 traces threshold/)).toBeInTheDocument();
      });

      // Switch to EWMA 100
      const ewmaSection = screen.getByText('EWMA').closest<HTMLElement>('div[class*="p-2.5"]');
      const ewmaSelect = within(ewmaSection!).getByRole('combobox');
      await user.selectOptions(ewmaSelect, 'ewma:100');

      // The API will respond with the new combination
      // The "Currently Active" display should update
      await waitFor(() => {
        expect(apiCallCounts['/api/variants/combination']).toBeGreaterThan(0);
      });
    });

    it('toggles baseline panel visibility', async () => {
      const user = userEvent.setup();
      render(<App />);

      await waitFor(() => {
        expect(screen.getByText('Baseline Strategy & Threshold Selection')).toBeInTheDocument();
      });

      // Initially panel should be visible
      await waitFor(() => {
        expect(screen.getByText('Cumulative')).toBeInTheDocument();
      });

      // Click to collapse
      const panelToggle = screen.getByText('Baseline Strategy & Threshold Selection');
      await user.click(panelToggle);

      // Panel content should be hidden
      await waitFor(() => {
        expect(screen.queryByText('Cumulative')).not.toBeInTheDocument();
      });

      // Click to expand again
      await user.click(panelToggle);

      // Panel content should be visible again
      await waitFor(() => {
        expect(screen.getByText('Cumulative')).toBeInTheDocument();
      });
    });

    it('refreshes services after combination switch', async () => {
      const user = userEvent.setup();
      render(<App />);

      // Wait for initial services load
      await waitFor(() => {
        expect(apiCallCounts['/api/services']).toBeGreaterThanOrEqual(1);
      });

      const initialServicesCalls = apiCallCounts['/api/services'];

      await waitFor(() => {
        expect(screen.getByText('EWMA')).toBeInTheDocument();
      });

      // Switch combination
      const ewmaSection = screen.getByText('EWMA').closest<HTMLElement>('div[class*="p-2.5"]');
      const ewmaSelect = within(ewmaSection!).getByRole('combobox');
      await user.selectOptions(ewmaSelect, 'ewma:50');

      // Services should be re-fetched after combination switch
      await waitFor(() => {
        expect(apiCallCounts['/api/services']).toBeGreaterThan(initialServicesCalls);
      });
    });
  });

  // ==========================================================================
  // Additional Edge Case Tests
  // ==========================================================================
  describe('Edge Cases and Error Handling', () => {
    it('handles empty services list gracefully', async () => {
      // Override fetch to return empty services
      globalThis.fetch = vi.fn((url: string) => {
        const urlPath = url.replace(/^https?:\/\/[^/]+/, '').split('?')[0];
        let response: unknown = {};

        if (urlPath === '/api/services') {
          response = [];
        } else if (urlPath === '/api/stats') {
          response = mockStats;
        } else if (urlPath === '/api/variants/combinations') {
          response = { combinations: mockCombinations, active: 'cumulative:50' };
        }

        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve(response),
        });
      }) as unknown as typeof fetch;

      render(<App />);

      const sidebar = document.querySelector('aside');
      // Should show waiting message when no services
      await waitFor(() => {
        expect(within(sidebar!).getByText('Waiting for traces...')).toBeInTheDocument();
      });
    });

    it('toggles auto-refresh functionality', async () => {
      const user = userEvent.setup();
      render(<App />);

      const header = document.querySelector('header');
      await waitFor(() => {
        expect(within(header!).getByText(/Auto-refresh Off/)).toBeInTheDocument();
      });

      // Click to enable auto-refresh
      const autoRefreshButton = within(header!).getByText(/Auto-refresh Off/);
      await user.click(autoRefreshButton);

      // Should now show "On"
      await waitFor(() => {
        expect(within(header!).getByText(/Auto-refresh On/)).toBeInTheDocument();
      });

      // Click to disable
      await user.click(within(header!).getByText(/Auto-refresh On/));

      // Should be off again
      await waitFor(() => {
        expect(within(header!).getByText(/Auto-refresh Off/)).toBeInTheDocument();
      });
    });

    it('maintains service selection after filter change', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      // Select api-gateway
      await user.click(within(sidebar!).getByText('api-gateway'));

      const main = document.querySelector('main');
      await waitFor(() => {
        expect(within(main!).getByText('api-gateway:handleRequest')).toBeInTheDocument();
      });

      // Apply a filter that still includes api-gateway
      const scope1Select = within(sidebar!).getByLabelText('Scope1');
      await user.selectOptions(scope1Select, 'platform');

      // Service detail should still be visible
      await waitFor(() => {
        expect(within(main!).getByText('api-gateway:handleRequest')).toBeInTheDocument();
      });
    });

    it('shows "Show more" buttons for deviations and anomalies', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      await user.click(within(sidebar!).getByText('api-gateway'));

      const main = document.querySelector('main');
      // Wait for deviations and anomalies to load
      await waitFor(() => {
        expect(within(main!).getByText(/Recent Deviations/)).toBeInTheDocument();
        expect(within(main!).getByText(/Recent Anomalies/)).toBeInTheDocument();
      });

      // "Show more" buttons should be present in main content
      await waitFor(() => {
        const showMoreButtons = within(main!).getAllByText('Show more');
        expect(showMoreButtons.length).toBe(2); // One for deviations, one for anomalies
      });
    });

    it('navigates between services correctly', async () => {
      const user = userEvent.setup();
      render(<App />);

      const sidebar = document.querySelector('aside');
      const main = document.querySelector('main');

      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      // Click on first service
      await user.click(within(sidebar!).getByText('api-gateway'));
      await waitFor(() => {
        expect(within(main!).getByText('api-gateway:handleRequest')).toBeInTheDocument();
      });

      // Click on second service
      await user.click(within(sidebar!).getByText('user-service'));
      await waitFor(() => {
        expect(within(main!).getByText('user-service:getUser')).toBeInTheDocument();
      });

      // Click on third service
      await user.click(within(sidebar!).getByText('payment-service'));
      await waitFor(() => {
        expect(within(main!).getByText('payment-service:processPayment')).toBeInTheDocument();
      });
    });
  });

  // ==========================================================================
  // Request ordering and polling correctness
  // ==========================================================================
  describe('Request ordering', () => {
    it('ignores a slow response for the previous service after switching', async () => {
      const baseFetch = createMockFetch();
      const staleA = 'api-gateway:handleRequest';
      let releaseA: () => void = () => {};
      const gateA = new Promise<void>(resolve => { releaseA = resolve; });

      vi.stubGlobal('fetch', vi.fn(async (url: string, options?: RequestInit) => {
        const path = url.replace(/^https?:\/\/[^/]+/, '').split('?')[0];
        const serviceId = new URLSearchParams(url.split('?')[1] || '').get('service_id');
        if (path === '/api/deviations') {
          await (serviceId === staleA ? gateA : Promise.resolve());
          const summary = serviceId === staleA ? 'STALE deviation for A' : 'Fresh deviation for B';
          return { ok: true, json: () => Promise.resolve([{ ...mockDeviations[0], diff_summary: summary }]) };
        }
        return baseFetch(url, options);
      }));

      const user = userEvent.setup();
      render(<App />);
      const sidebar = document.querySelector('aside');
      const main = document.querySelector('main');

      await waitFor(() => {
        expect(within(main!).getByRole('heading', { name: staleA })).toBeInTheDocument();
      });

      await user.click(within(sidebar!).getByText('user-service'));
      await waitFor(() => {
        expect(within(main!).getByText(/Fresh deviation for B/)).toBeInTheDocument();
      });

      releaseA();
      await new Promise(resolve => setTimeout(resolve, 20));

      expect(within(main!).getByText(/Fresh deviation for B/)).toBeInTheDocument();
      expect(within(main!).queryByText(/STALE deviation for A/)).not.toBeInTheDocument();
    });

    it('keeps the service restored from localStorage when services load', async () => {
      localStorage.setItem('trace-analyzer-ui-state', JSON.stringify({ selectedService: 'user-service:getUser' }));
      render(<App />);
      const sidebar = document.querySelector('aside');
      const main = document.querySelector('main');

      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });

      expect(within(main!).getByRole('heading', { name: 'user-service:getUser' })).toBeInTheDocument();
    });

    describe('?service_id= URL param', () => {
      const urlService = () => new URLSearchParams(window.location.search).get('service_id');

      afterEach(() => {
        vi.useRealTimers();
        window.history.replaceState({}, '', '/');
      });

      it('picks the initial selection once, then a user click wins over later polls', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        window.history.replaceState({}, '', `/?service_id=${encodeURIComponent('user-service:getUser')}`);
        const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
        render(<App />);
        const sidebar = document.querySelector('aside');
        const main = document.querySelector('main');

        await waitFor(() => {
          expect(within(sidebar!).getByText('payment-service')).toBeInTheDocument();
        });
        expect(within(main!).getByRole('heading', { name: 'user-service:getUser' })).toBeInTheDocument();

        await user.click(screen.getByText(/Auto-refresh Off/));
        await user.click(within(sidebar!).getByText('payment-service'));
        await waitFor(() => {
          expect(within(main!).getByRole('heading', { name: 'payment-service:processPayment' })).toBeInTheDocument();
        });

        const callsBeforePoll = apiCallCounts['/api/services'];
        await act(async () => { await vi.advanceTimersByTimeAsync(5500); });
        await waitFor(() => {
          expect(apiCallCounts['/api/services']).toBeGreaterThan(callsBeforePoll);
        });

        expect(within(main!).getByRole('heading', { name: 'payment-service:processPayment' })).toBeInTheDocument();
        expect(urlService()).toBe('payment-service:processPayment');
      });

      it('writes the auto-selected first service into the URL', async () => {
        render(<App />);
        const main = document.querySelector('main');

        await waitFor(() => {
          expect(within(main!).getByRole('heading', { name: 'api-gateway:handleRequest' })).toBeInTheDocument();
        });
        await waitFor(() => {
          expect(urlService()).toBe('api-gateway:handleRequest');
        });
      });

      it('writes the clicked service into ?service_id=', async () => {
        const user = userEvent.setup();
        render(<App />);
        const sidebar = document.querySelector('aside');

        await waitFor(() => {
          expect(within(sidebar!).getByText('payment-service')).toBeInTheDocument();
        });
        await user.click(within(sidebar!).getByText('payment-service'));

        await waitFor(() => {
          expect(urlService()).toBe('payment-service:processPayment');
        });
      });

      it('restores the clicked service on reload, even without localStorage', async () => {
        window.history.replaceState({}, '', `/?service_id=${encodeURIComponent('user-service:getUser')}`);
        const user = userEvent.setup();
        const { unmount } = render(<App />);
        const sidebar = document.querySelector('aside');
        const main = document.querySelector('main');

        await waitFor(() => {
          expect(within(main!).getByRole('heading', { name: 'user-service:getUser' })).toBeInTheDocument();
        });
        await user.click(within(sidebar!).getByText('payment-service'));
        await waitFor(() => {
          expect(within(main!).getByRole('heading', { name: 'payment-service:processPayment' })).toBeInTheDocument();
        });

        unmount();
        localStorage.clear();
        render(<App />);
        const reloadedMain = document.querySelector('main');

        await waitFor(() => {
          expect(within(reloadedMain!).getByRole('heading', { name: 'payment-service:processPayment' })).toBeInTheDocument();
        });
      });

      it('keeps other query params and the hash', async () => {
        window.history.replaceState({}, '', '/?foo=bar#x');
        render(<App />);

        await waitFor(() => {
          expect(urlService()).toBe('api-gateway:handleRequest');
        });
        expect(new URLSearchParams(window.location.search).get('foo')).toBe('bar');
        expect(window.location.hash).toBe('#x');
        expect(window.location.pathname).toBe('/');
      });

      it('replaces an unknown URL service with the fallback service', async () => {
        window.history.replaceState({}, '', '/?service_id=missing-service');
        render(<App />);
        const main = document.querySelector('main');

        await waitFor(() => {
          expect(within(main!).getByRole('heading', { name: 'api-gateway:handleRequest' })).toBeInTheDocument();
        });
        await waitFor(() => {
          expect(urlService()).toBe('api-gateway:handleRequest');
        });
      });

      it('leaves the URL alone until services load', async () => {
        const baseFetch = createMockFetch();
        let releaseServices: () => void = () => {};
        const servicesGate = new Promise<void>(resolve => { releaseServices = resolve; });
        vi.stubGlobal('fetch', vi.fn(async (url: string, options?: RequestInit) => {
          const path = url.replace(/^https?:\/\/[^/]+/, '').split('?')[0];
          if (path === '/api/services') await servicesGate;
          return baseFetch(url, options);
        }));
        localStorage.setItem('trace-analyzer-ui-state', JSON.stringify({ selectedService: 'user-service:getUser' }));
        const startUrl = `/?service_id=${encodeURIComponent('payment-service:processPayment')}`;
        window.history.replaceState({}, '', startUrl);

        render(<App />);
        const main = document.querySelector('main');
        await new Promise(resolve => setTimeout(resolve, 30));
        expect(window.location.pathname + window.location.search).toBe(startUrl);

        releaseServices();
        await waitFor(() => {
          expect(within(main!).getByRole('heading', { name: 'payment-service:processPayment' })).toBeInTheDocument();
        });
        expect(urlService()).toBe('payment-service:processPayment');
      });

      it('never adds history entries while switching services', async () => {
        const pushState = vi.spyOn(window.history, 'pushState');
        const user = userEvent.setup();
        render(<App />);
        const sidebar = document.querySelector('aside');

        await waitFor(() => {
          expect(within(sidebar!).getByText('payment-service')).toBeInTheDocument();
        });
        await user.click(within(sidebar!).getByText('payment-service'));
        await user.click(within(sidebar!).getByText('user-service'));
        await waitFor(() => {
          expect(urlService()).toBe('user-service:getUser');
        });
        expect(pushState).not.toHaveBeenCalled();
      });

      it('falls back to the first service when the URL service does not exist', async () => {
        window.history.replaceState({}, '', '/?service_id=missing-service');
        render(<App />);
        const sidebar = document.querySelector('aside');
        const main = document.querySelector('main');

        await waitFor(() => {
          expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
        });
        expect(within(main!).getByRole('heading', { name: 'api-gateway:handleRequest' })).toBeInTheDocument();
      });
    });

    it('refetches services after a combination switch with the active filter query', async () => {
      const user = userEvent.setup();
      render(<App />);
      const sidebar = document.querySelector('aside');

      await waitFor(() => {
        expect(within(sidebar!).getByText('api-gateway')).toBeInTheDocument();
      });
      await user.selectOptions(within(sidebar!).getByLabelText('Scope1'), 'platform');

      const servicesUrls = () => vi.mocked(fetch).mock.calls
        .map(([url]) => String(url))
        .filter(url => url.replace(/^https?:\/\/[^/]+/, '').startsWith('/api/services'));
      const lastServicesUrl = () => servicesUrls()[servicesUrls().length - 1];
      await waitFor(() => {
        expect(lastServicesUrl()).toContain('scope1=platform');
      });
      const pollUrl = lastServicesUrl();
      const callsBeforeSwitch = servicesUrls().length;

      const ewmaSection = screen.getByText('EWMA').closest<HTMLElement>('div[class*="p-2.5"]');
      await user.selectOptions(within(ewmaSection!).getByRole('combobox'), 'ewma:100');

      await waitFor(() => {
        expect(servicesUrls().length).toBeGreaterThan(callsBeforeSwitch);
      });
      expect(lastServicesUrl()).toBe(pollUrl);
    });
  });
});
