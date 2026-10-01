import '@testing-library/jest-dom';
import { vi } from 'vitest';

// Mock ResizeObserver for recharts
if (!globalThis.ResizeObserver) {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}

// Mock fetch for API calls
const mockResponses = {
  '/api/services': [
    {
      service_id: 'test-service',
      trace_count: 100,
      topology_count: 5,
      service_grouping: {
        service_identity: 'test-service',
        scope1: 'platform',
        scope2: 'api',
        scope3: 'v1',
        env: 'prod'
      }
    }
  ],
  '/api/stats': {
    hot_tier_traces: 1000,
    unique_services: 10,
    unique_topologies: 50,
    total_deviations: 5,
    total_anomalies: 2,
    total_errors: 1,
    interesting_traces: 10,
    interesting_traces_pct: 1.0
  },
  '/api/variants': { variants: ['cumulative', 'ewma'], active: 'cumulative' },
  '/api/variants/combinations': {
    combinations: [
      { combination: 'cumulative:50', variant: 'cumulative' },
      { combination: 'cumulative:100', variant: 'cumulative' }
    ],
    active: 'cumulative:50'
  },
  '/api/topologies': [],
  '/api/deviations': [],
  '/api/anomalies': []
};

globalThis.fetch = vi.fn((url) => {
  const path = url.replace(/^https?:\/\/[^/]+/, '').split('?')[0];
  const response = mockResponses[path] || {};

  return Promise.resolve({
    ok: true,
    json: () => Promise.resolve(response)
  });
});

// Mock localStorage
const localStorageMock = {
  store: {},
  getItem: vi.fn((key) => localStorageMock.store[key] || null),
  setItem: vi.fn((key, value) => { localStorageMock.store[key] = value; }),
  removeItem: vi.fn((key) => { delete localStorageMock.store[key]; }),
  clear: vi.fn(() => { localStorageMock.store = {}; })
};
globalThis.localStorage = localStorageMock;

// Reset mocks before each test
beforeEach(() => {
  vi.clearAllMocks();
  localStorageMock.store = {};
});
