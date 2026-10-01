import { render, screen, fireEvent, within } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { Sidebar, SidebarProps, SidebarFilters, SidebarFilterOptions } from './Sidebar';
import type { Service } from '../../types';

// Mock Services
const mockServices: Service[] = [
  {
    service_id: 'service-1-hash',
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
    topology_count: 8,
    avg_duration_us: 25000,
    p50_duration_us: 20000,
    p75_duration_us: 30000,
    error_rate: 0.02,
    last_seen: '2024-01-15T10:30:00Z',
    deviation_count: 5,
    anomaly_count: 2,
  },
  {
    service_id: 'service-2-hash',
    service_grouping: {
      service_identity: 'user-service',
      scope1: 'platform',
      scope2: 'backend',
      scope3: 'api',
      env: 'prod',
      operation: 'getUser',
      feature_group: 'users',
      feature_name: 'auth',
      sub_service: 'main',
    },
    trace_count: 2500,
    topology_count: 12,
    avg_duration_us: 15000,
    p50_duration_us: 12000,
    p75_duration_us: 20000,
    error_rate: 0.01,
    last_seen: '2024-01-15T10:25:00Z',
    deviation_count: 3,
    anomaly_count: 1,
  },
  {
    service_id: 'service-3-hash',
    service_grouping: {
      service_identity: 'order-service',
      scope1: 'commerce',
      scope2: 'backend',
      scope3: 'api',
      env: 'staging',
      operation: 'createOrder',
      feature_group: 'orders',
      feature_name: 'checkout',
      sub_service: 'main',
    },
    trace_count: 800,
    topology_count: 6,
    avg_duration_us: 45000,
    p50_duration_us: 40000,
    p75_duration_us: 55000,
    error_rate: 0.05,
    last_seen: '2024-01-15T10:20:00Z',
    deviation_count: 0,
    anomaly_count: 0,
  },
];

const defaultFilters: SidebarFilters = {
  serviceSearch: '',
  scope1Filter: '',
  scope2Filter: '',
  scope3Filter: '',
  envFilter: '',
  serviceIdentityFilter: '',
  filterErrors: false,
  filterDeviations: false,
  filterAnomalies: false,
};

const defaultFilterOptions: SidebarFilterOptions = {
  scope1Options: ['platform', 'commerce'],
  scope2Options: ['frontend', 'backend'],
  scope3Options: ['web', 'api'],
  envOptions: ['prod', 'staging', 'dev'],
};

const defaultProps: SidebarProps = {
  services: mockServices,
  selectedService: null,
  onServiceSelect: vi.fn(),
  filters: defaultFilters,
  onFilterChange: vi.fn(),
  filterOptions: defaultFilterOptions,
};

describe('Sidebar', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Mock window.open for the open in new tab functionality
    vi.stubGlobal('open', vi.fn());
  });

  describe('Service List Rendering', () => {
    it('renders the Services heading', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByText('Services')).toBeInTheDocument();
    });

    it('renders all services in the list', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByText('api-gateway')).toBeInTheDocument();
      expect(screen.getByText('user-service')).toBeInTheDocument();
      expect(screen.getByText('order-service')).toBeInTheDocument();
    });

    it('displays service trace and topology counts', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByText('1500 traces · 8 topologies')).toBeInTheDocument();
      expect(screen.getByText('2500 traces · 12 topologies')).toBeInTheDocument();
      expect(screen.getByText('800 traces · 6 topologies')).toBeInTheDocument();
    });

    it('displays service deviation and anomaly counts', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByText('5 deviations · 2 anomalies')).toBeInTheDocument();
      expect(screen.getByText('3 deviations · 1 anomalies')).toBeInTheDocument();
      expect(screen.getByText('0 deviations · 0 anomalies')).toBeInTheDocument();
    });

    it('displays scope path for each service', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByText('platform / frontend / web · env=prod')).toBeInTheDocument();
      expect(screen.getByText('platform / backend / api · env=prod')).toBeInTheDocument();
      expect(screen.getByText('commerce / backend / api · env=staging')).toBeInTheDocument();
    });

    it('displays service ID for each service', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByText('id: service-1-hash')).toBeInTheDocument();
      expect(screen.getByText('id: service-2-hash')).toBeInTheDocument();
      expect(screen.getByText('id: service-3-hash')).toBeInTheDocument();
    });

    it('highlights selected service', () => {
      render(<Sidebar {...defaultProps} selectedService="service-1-hash" />);
      const serviceItem = screen.getByText('api-gateway').closest('[role="button"]');
      expect(serviceItem).toHaveClass('bg-emerald-500/20');
    });

    it('does not highlight non-selected services', () => {
      render(<Sidebar {...defaultProps} selectedService="service-1-hash" />);
      const serviceItem = screen.getByText('user-service').closest('[role="button"]');
      expect(serviceItem).not.toHaveClass('bg-emerald-500/20');
    });
  });

  describe('Filter Input', () => {
    it('renders the filter input with correct placeholder', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByPlaceholderText('Filter services...')).toBeInTheDocument();
    });

    it('displays current filter value in input', () => {
      const filters = { ...defaultFilters, serviceSearch: 'api' };
      render(<Sidebar {...defaultProps} filters={filters} />);
      const input = screen.getByPlaceholderText('Filter services...');
      expect(input).toHaveValue('api');
    });

    it('calls onFilterChange when typing in filter input', () => {
      const onFilterChange = vi.fn();
      render(<Sidebar {...defaultProps} onFilterChange={onFilterChange} />);

      const input = screen.getByPlaceholderText('Filter services...');
      fireEvent.change(input, { target: { value: 'user' } });

      expect(onFilterChange).toHaveBeenCalledWith('serviceSearch', 'user');
    });

    it('calls onFilterChange with empty string when clearing filter', () => {
      const onFilterChange = vi.fn();
      const filters = { ...defaultFilters, serviceSearch: 'api' };
      render(<Sidebar {...defaultProps} filters={filters} onFilterChange={onFilterChange} />);

      const input = screen.getByPlaceholderText('Filter services...');
      fireEvent.change(input, { target: { value: '' } });

      expect(onFilterChange).toHaveBeenCalledWith('serviceSearch', '');
    });
  });

  describe('Dropdown Filters', () => {
    it('renders Scope1 dropdown', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByLabelText('Scope1')).toBeInTheDocument();
    });

    it('renders Scope2 dropdown', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByLabelText('Scope2')).toBeInTheDocument();
    });

    it('renders Scope3 dropdown', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByLabelText('Scope3')).toBeInTheDocument();
    });

    it('renders Env dropdown', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByLabelText('Env')).toBeInTheDocument();
    });

    it('renders Service Identity input', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByLabelText('Service Identity')).toBeInTheDocument();
    });

    it('Scope1 dropdown contains correct options', () => {
      render(<Sidebar {...defaultProps} />);
      const select = screen.getByLabelText('Scope1');
      const options = within(select).getAllByRole('option');

      expect(options).toHaveLength(3); // "All" + 2 options
      expect(options[0]).toHaveTextContent('All');
      expect(options[1]).toHaveTextContent('platform');
      expect(options[2]).toHaveTextContent('commerce');
    });

    it('Scope2 dropdown contains correct options', () => {
      render(<Sidebar {...defaultProps} />);
      const select = screen.getByLabelText('Scope2');
      const options = within(select).getAllByRole('option');

      expect(options).toHaveLength(3); // "All" + 2 options
      expect(options[0]).toHaveTextContent('All');
      expect(options[1]).toHaveTextContent('frontend');
      expect(options[2]).toHaveTextContent('backend');
    });

    it('Env dropdown contains correct options', () => {
      render(<Sidebar {...defaultProps} />);
      const select = screen.getByLabelText('Env');
      const options = within(select).getAllByRole('option');

      expect(options).toHaveLength(4); // "All" + 3 options
      expect(options[0]).toHaveTextContent('All');
      expect(options[1]).toHaveTextContent('prod');
      expect(options[2]).toHaveTextContent('staging');
      expect(options[3]).toHaveTextContent('dev');
    });

    it('calls onFilterChange when Scope1 is changed', () => {
      const onFilterChange = vi.fn();
      render(<Sidebar {...defaultProps} onFilterChange={onFilterChange} />);

      const select = screen.getByLabelText('Scope1');
      fireEvent.change(select, { target: { value: 'platform' } });

      expect(onFilterChange).toHaveBeenCalledWith('scope1Filter', 'platform');
    });

    it('calls onFilterChange when Scope2 is changed', () => {
      const onFilterChange = vi.fn();
      render(<Sidebar {...defaultProps} onFilterChange={onFilterChange} />);

      const select = screen.getByLabelText('Scope2');
      fireEvent.change(select, { target: { value: 'backend' } });

      expect(onFilterChange).toHaveBeenCalledWith('scope2Filter', 'backend');
    });

    it('calls onFilterChange when Scope3 is changed', () => {
      const onFilterChange = vi.fn();
      render(<Sidebar {...defaultProps} onFilterChange={onFilterChange} />);

      const select = screen.getByLabelText('Scope3');
      fireEvent.change(select, { target: { value: 'api' } });

      expect(onFilterChange).toHaveBeenCalledWith('scope3Filter', 'api');
    });

    it('calls onFilterChange when Env is changed', () => {
      const onFilterChange = vi.fn();
      render(<Sidebar {...defaultProps} onFilterChange={onFilterChange} />);

      const select = screen.getByLabelText('Env');
      fireEvent.change(select, { target: { value: 'staging' } });

      expect(onFilterChange).toHaveBeenCalledWith('envFilter', 'staging');
    });

    it('calls onFilterChange when Service Identity is typed', () => {
      const onFilterChange = vi.fn();
      render(<Sidebar {...defaultProps} onFilterChange={onFilterChange} />);

      const input = screen.getByLabelText('Service Identity');
      fireEvent.change(input, { target: { value: 'api-gateway' } });

      expect(onFilterChange).toHaveBeenCalledWith('serviceIdentityFilter', 'api-gateway');
    });

    it('displays selected filter value in dropdown', () => {
      const filters = { ...defaultFilters, scope1Filter: 'platform' };
      render(<Sidebar {...defaultProps} filters={filters} />);

      const select = screen.getByLabelText('Scope1');
      expect(select).toHaveValue('platform');
    });
  });

  describe('Checkbox Filters', () => {
    it('renders Errors only checkbox', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByLabelText('Errors only')).toBeInTheDocument();
    });

    it('renders Deviations only checkbox', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByLabelText('Deviations only')).toBeInTheDocument();
    });

    it('renders Anomalies only checkbox', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByLabelText('Anomalies only')).toBeInTheDocument();
    });

    it('Errors checkbox is unchecked by default', () => {
      render(<Sidebar {...defaultProps} />);
      const checkbox = screen.getByLabelText('Errors only');
      expect(checkbox).not.toBeChecked();
    });

    it('Deviations checkbox is unchecked by default', () => {
      render(<Sidebar {...defaultProps} />);
      const checkbox = screen.getByLabelText('Deviations only');
      expect(checkbox).not.toBeChecked();
    });

    it('Anomalies checkbox is unchecked by default', () => {
      render(<Sidebar {...defaultProps} />);
      const checkbox = screen.getByLabelText('Anomalies only');
      expect(checkbox).not.toBeChecked();
    });

    it('displays checked state correctly for Errors filter', () => {
      const filters = { ...defaultFilters, filterErrors: true };
      render(<Sidebar {...defaultProps} filters={filters} />);

      const checkbox = screen.getByLabelText('Errors only');
      expect(checkbox).toBeChecked();
    });

    it('displays checked state correctly for Deviations filter', () => {
      const filters = { ...defaultFilters, filterDeviations: true };
      render(<Sidebar {...defaultProps} filters={filters} />);

      const checkbox = screen.getByLabelText('Deviations only');
      expect(checkbox).toBeChecked();
    });

    it('displays checked state correctly for Anomalies filter', () => {
      const filters = { ...defaultFilters, filterAnomalies: true };
      render(<Sidebar {...defaultProps} filters={filters} />);

      const checkbox = screen.getByLabelText('Anomalies only');
      expect(checkbox).toBeChecked();
    });

    it('calls onFilterChange when Errors checkbox is clicked', () => {
      const onFilterChange = vi.fn();
      render(<Sidebar {...defaultProps} onFilterChange={onFilterChange} />);

      const checkbox = screen.getByLabelText('Errors only');
      fireEvent.click(checkbox);

      expect(onFilterChange).toHaveBeenCalledWith('filterErrors', true);
    });

    it('calls onFilterChange when Deviations checkbox is clicked', () => {
      const onFilterChange = vi.fn();
      render(<Sidebar {...defaultProps} onFilterChange={onFilterChange} />);

      const checkbox = screen.getByLabelText('Deviations only');
      fireEvent.click(checkbox);

      expect(onFilterChange).toHaveBeenCalledWith('filterDeviations', true);
    });

    it('calls onFilterChange when Anomalies checkbox is clicked', () => {
      const onFilterChange = vi.fn();
      render(<Sidebar {...defaultProps} onFilterChange={onFilterChange} />);

      const checkbox = screen.getByLabelText('Anomalies only');
      fireEvent.click(checkbox);

      expect(onFilterChange).toHaveBeenCalledWith('filterAnomalies', true);
    });

    it('calls onFilterChange with false when unchecking a checkbox', () => {
      const onFilterChange = vi.fn();
      const filters = { ...defaultFilters, filterErrors: true };
      render(<Sidebar {...defaultProps} filters={filters} onFilterChange={onFilterChange} />);

      const checkbox = screen.getByLabelText('Errors only');
      fireEvent.click(checkbox);

      expect(onFilterChange).toHaveBeenCalledWith('filterErrors', false);
    });
  });

  describe('Service Selection Callback', () => {
    it('calls onServiceSelect when a service is clicked', () => {
      const onServiceSelect = vi.fn();
      render(<Sidebar {...defaultProps} onServiceSelect={onServiceSelect} />);

      const serviceItem = screen.getByText('api-gateway').closest('[role="button"]');
      if (serviceItem) {
        fireEvent.click(serviceItem);
      }

      expect(onServiceSelect).toHaveBeenCalledWith('service-1-hash');
    });

    it('calls onServiceSelect with correct service ID for each service', () => {
      const onServiceSelect = vi.fn();
      render(<Sidebar {...defaultProps} onServiceSelect={onServiceSelect} />);

      const userServiceItem = screen.getByText('user-service').closest('[role="button"]');
      if (userServiceItem) {
        fireEvent.click(userServiceItem);
      }

      expect(onServiceSelect).toHaveBeenCalledWith('service-2-hash');
    });

    it('supports keyboard navigation with Enter key', () => {
      const onServiceSelect = vi.fn();
      render(<Sidebar {...defaultProps} onServiceSelect={onServiceSelect} />);

      const serviceItem = screen.getByText('api-gateway').closest('[role="button"]');
      if (serviceItem) {
        fireEvent.keyDown(serviceItem, { key: 'Enter' });
      }

      expect(onServiceSelect).toHaveBeenCalledWith('service-1-hash');
    });

    it('supports keyboard navigation with Space key', () => {
      const onServiceSelect = vi.fn();
      render(<Sidebar {...defaultProps} onServiceSelect={onServiceSelect} />);

      const serviceItem = screen.getByText('api-gateway').closest('[role="button"]');
      if (serviceItem) {
        fireEvent.keyDown(serviceItem, { key: ' ' });
      }

      expect(onServiceSelect).toHaveBeenCalledWith('service-1-hash');
    });

    it('does not trigger selection on other keys', () => {
      const onServiceSelect = vi.fn();
      render(<Sidebar {...defaultProps} onServiceSelect={onServiceSelect} />);

      const serviceItem = screen.getByText('api-gateway').closest('[role="button"]');
      if (serviceItem) {
        fireEvent.keyDown(serviceItem, { key: 'Tab' });
      }

      expect(onServiceSelect).not.toHaveBeenCalled();
    });
  });

  describe('Empty State', () => {
    it('shows "Waiting for traces..." when services list is empty', () => {
      render(<Sidebar {...defaultProps} services={[]} />);
      expect(screen.getByText('Waiting for traces...')).toBeInTheDocument();
    });

    it('does not show service items when list is empty', () => {
      render(<Sidebar {...defaultProps} services={[]} />);
      expect(screen.queryByText('api-gateway')).not.toBeInTheDocument();
      expect(screen.queryByText('user-service')).not.toBeInTheDocument();
    });

    it('still renders filter controls when list is empty', () => {
      render(<Sidebar {...defaultProps} services={[]} />);
      expect(screen.getByPlaceholderText('Filter services...')).toBeInTheDocument();
      expect(screen.getByLabelText('Scope1')).toBeInTheDocument();
      expect(screen.getByLabelText('Errors only')).toBeInTheDocument();
    });
  });

  describe('Open in New Tab Functionality', () => {
    it('renders open in new tab button for each service', () => {
      render(<Sidebar {...defaultProps} />);
      // There should be one button with arrow icon for each service
      const openButtons = screen.getAllByTitle('Open in new tab');
      expect(openButtons).toHaveLength(3);
    });

    it('opens service in new tab when button is clicked', () => {
      const mockOpen = vi.fn();
      vi.stubGlobal('open', mockOpen);

      render(<Sidebar {...defaultProps} />);

      const openButtons = screen.getAllByTitle('Open in new tab');
      fireEvent.click(openButtons[0]);

      expect(mockOpen).toHaveBeenCalledWith(
        expect.stringContaining('service_id=service-1-hash'),
        '_blank',
        'noopener,noreferrer'
      );
    });

    it('keeps the current path and other params in the new tab link', () => {
      const mockOpen = vi.fn();
      vi.stubGlobal('open', mockOpen);
      window.history.replaceState({}, '', '/ui/?foo=bar&service_id=old#section');

      try {
        render(<Sidebar {...defaultProps} />);
        fireEvent.click(screen.getAllByTitle('Open in new tab')[0]);
      } finally {
        window.history.replaceState({}, '', '/');
      }

      const opened = new URL(mockOpen.mock.calls[0][0]);
      expect(opened.pathname).toBe('/ui/');
      expect(opened.searchParams.get('foo')).toBe('bar');
      expect(opened.searchParams.get('service_id')).toBe('service-1-hash');
      expect(opened.hash).toBe('');
    });

    it('does not trigger service selection when open button is clicked', () => {
      const onServiceSelect = vi.fn();
      render(<Sidebar {...defaultProps} onServiceSelect={onServiceSelect} />);

      const openButtons = screen.getAllByTitle('Open in new tab');
      fireEvent.click(openButtons[0]);

      expect(onServiceSelect).not.toHaveBeenCalled();
    });
  });

  describe('Service Without Grouping', () => {
    it('handles service without service_grouping gracefully', () => {
      const serviceWithoutGrouping: Service = {
        service_id: 'service-no-grouping',
        service_grouping: undefined as any,
        trace_count: 100,
        topology_count: 3,
        avg_duration_us: 10000,
        p50_duration_us: 8000,
        p75_duration_us: 12000,
        error_rate: 0.01,
        last_seen: '2024-01-15T10:00:00Z',
        deviation_count: 1,
        anomaly_count: 0,
      };

      render(<Sidebar {...defaultProps} services={[serviceWithoutGrouping]} />);
      expect(screen.getByText('service-no-grouping')).toBeInTheDocument();
      expect(screen.getByText('- / - / -')).toBeInTheDocument();
    });

    it('displays service_id when service_identity is missing', () => {
      const serviceWithoutIdentity: Service = {
        service_id: 'fallback-service-id',
        service_grouping: {
          service_identity: '',
          scope1: 'test',
          scope2: 'test',
          scope3: 'test',
          env: 'test',
          operation: '',
          feature_group: '',
          feature_name: '',
          sub_service: '',
        },
        trace_count: 100,
        topology_count: 3,
        avg_duration_us: 10000,
        p50_duration_us: 8000,
        p75_duration_us: 12000,
        error_rate: 0.01,
        last_seen: '2024-01-15T10:00:00Z',
        deviation_count: 1,
        anomaly_count: 0,
      };

      render(<Sidebar {...defaultProps} services={[serviceWithoutIdentity]} />);
      expect(screen.getByText('fallback-service-id')).toBeInTheDocument();
    });
  });

  describe('Accessibility', () => {
    it('sidebar is an aside element', () => {
      render(<Sidebar {...defaultProps} />);
      const sidebar = document.querySelector('aside');
      expect(sidebar).toBeInTheDocument();
    });

    it('service items have role="button"', () => {
      render(<Sidebar {...defaultProps} />);
      const serviceItems = screen.getAllByRole('button');
      // Filter buttons (open in new tab) + service items
      expect(serviceItems.length).toBeGreaterThanOrEqual(3);
    });

    it('service items have tabIndex for keyboard navigation', () => {
      render(<Sidebar {...defaultProps} />);
      const serviceItem = screen.getByText('api-gateway').closest('[role="button"]');
      expect(serviceItem).toHaveAttribute('tabIndex', '0');
    });

    it('filter dropdowns have proper labels', () => {
      render(<Sidebar {...defaultProps} />);

      const scope1 = screen.getByLabelText('Scope1');
      expect(scope1.tagName).toBe('SELECT');

      const scope2 = screen.getByLabelText('Scope2');
      expect(scope2.tagName).toBe('SELECT');
    });

    it('checkboxes have proper labels', () => {
      render(<Sidebar {...defaultProps} />);

      const errorsCheckbox = screen.getByLabelText('Errors only');
      expect(errorsCheckbox.tagName).toBe('INPUT');
      expect(errorsCheckbox).toHaveAttribute('type', 'checkbox');
    });

    it('service items have title with full service info', () => {
      render(<Sidebar {...defaultProps} />);
      const serviceItem = screen.getByText('api-gateway').closest('[role="button"]');
      expect(serviceItem).toHaveAttribute('title');
      expect(serviceItem?.getAttribute('title')).toContain('service_identity=api-gateway');
    });
  });

  describe('Edge Cases', () => {
    it('handles very long service names with truncation', () => {
      const longNameService: Service = {
        service_id: 'very-long-service-name-that-might-overflow',
        service_grouping: {
          service_identity: 'a-very-long-service-identity-name-that-should-be-truncated-properly',
          scope1: 'platform',
          scope2: 'backend',
          scope3: 'api',
          env: 'production-east-us-2',
          operation: '',
          feature_group: '',
          feature_name: '',
          sub_service: '',
        },
        trace_count: 100,
        topology_count: 3,
        avg_duration_us: 10000,
        p50_duration_us: 8000,
        p75_duration_us: 12000,
        error_rate: 0.01,
        last_seen: '2024-01-15T10:00:00Z',
        deviation_count: 1,
        anomaly_count: 0,
      };

      render(<Sidebar {...defaultProps} services={[longNameService]} />);
      expect(screen.getByText('a-very-long-service-identity-name-that-should-be-truncated-properly')).toBeInTheDocument();
    });

    it('handles services with zero deviation and anomaly counts', () => {
      render(<Sidebar {...defaultProps} />);
      expect(screen.getByText('0 deviations · 0 anomalies')).toBeInTheDocument();
    });

    it('handles empty filter options gracefully', () => {
      const emptyFilterOptions: SidebarFilterOptions = {
        scope1Options: [],
        scope2Options: [],
        scope3Options: [],
        envOptions: [],
      };

      render(<Sidebar {...defaultProps} filterOptions={emptyFilterOptions} />);

      const scope1 = screen.getByLabelText('Scope1');
      const options = within(scope1).getAllByRole('option');
      expect(options).toHaveLength(1); // Only "All" option
    });

    it('maintains scroll position within service list', () => {
      render(<Sidebar {...defaultProps} />);
      const scrollContainer = document.querySelector('.overflow-y-auto');
      expect(scrollContainer).toBeInTheDocument();
    });
  });
});
