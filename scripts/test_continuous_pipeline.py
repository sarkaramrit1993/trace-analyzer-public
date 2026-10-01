#!/usr/bin/env python3
"""
Unit tests for the Continuous Pipeline module.

This test suite provides comprehensive coverage for the continuous-pipeline.py
module, including:
- SpanConverter functionality
- Span validation logic
- Batch sending with retry and backpressure
- Memory management and cleanup
- Metrics tracking and thread safety

Usage:
    pytest test_continuous_pipeline.py -v
    pytest test_continuous_pipeline.py -v -k "test_convert"  # Run specific tests
    pytest test_continuous_pipeline.py -v --cov=continuous-pipeline  # With coverage

Requirements:
    - pytest
    - pytest-mock
    - requests-mock (optional, we use pytest-mock for more control)
"""

import importlib.util
import sys
import threading
import time
from pathlib import Path
from typing import Any, Dict
from unittest.mock import MagicMock, Mock, patch, PropertyMock

import pytest


# Load the module with hyphenated name
def load_pipeline_module():
    """Load the continuous-pipeline module which has a hyphen in its name."""
    module_path = Path(__file__).parent / "continuous-pipeline.py"
    spec = importlib.util.spec_from_file_location("continuous_pipeline", module_path)
    module = importlib.util.module_from_spec(spec)
    sys.modules["continuous_pipeline"] = module
    spec.loader.exec_module(module)
    return module


# Load the module
pipeline_module = load_pipeline_module()
ContinuousPipeline = pipeline_module.ContinuousPipeline
PipelineMetrics = pipeline_module.PipelineMetrics


# =============================================================================
# Fixtures
# =============================================================================


@pytest.fixture
def mock_boto3():
    """Mock boto3 client for S3 operations."""
    with patch.object(pipeline_module, 'boto3') as mock:
        mock_client = MagicMock()
        mock.client.return_value = mock_client
        yield mock_client


@pytest.fixture
def mock_requests_session():
    """Mock requests.Session for HTTP operations."""
    with patch.object(pipeline_module.requests, 'Session') as mock_session_class:
        mock_session = MagicMock()
        mock_session_class.return_value = mock_session
        yield mock_session


@pytest.fixture
def pipeline(mock_boto3, mock_requests_session):
    """Create a ContinuousPipeline instance with mocked dependencies."""
    pipe = ContinuousPipeline(
        s3_bucket='test-bucket',
        analyzer_url='http://localhost:8080',
        s3_prefix='spans/',
        batch_size=100,
        poll_interval=10,
        max_retries=3,
        request_timeout=30,
    )
    return pipe


@pytest.fixture
def valid_row():
    """Return a valid parquet row for testing."""
    return {
        'trace_id': 'abc123def456',
        'span_id': 'span-001',
        'parent_id': 'parent-001',
        'name': 'GET /api/users',
        'timestamp_micros': 1700000000000000,
        'duration_micros': 150000,
        'local_endpoint_service_name': 'user-service',
        'http.method': 'GET',
        'http.status_code': '200',
    }


@pytest.fixture
def metrics():
    """Create a fresh PipelineMetrics instance."""
    return PipelineMetrics()


# =============================================================================
# SpanConverter Tests
# =============================================================================


class TestSpanConverter:
    """Tests for the _convert_row_to_span method."""

    def test_convert_row_to_span_with_valid_data(self, pipeline, valid_row):
        """Test conversion of a valid row to span format.

        Verifies that all fields are properly extracted and mapped to the
        expected span format used by the trace analyzer.
        """
        span = pipeline._convert_row_to_span(valid_row)

        assert span is not None
        assert span['TraceID'] == 'abc123def456'
        assert span['SpanID'] == 'span-001'
        assert span['ParentID'] == 'parent-001'
        assert span['OperationName'] == 'GET /api/users'
        assert span['StartTime'] == 1700000000000000
        assert span['Duration'] == 150000
        assert span['ServiceName'] == 'user-service'
        assert 'Tags' in span
        assert span['Tags'].get('http.method') == 'GET'
        assert span['Tags'].get('http.status_code') == '200'

    def test_convert_row_to_span_missing_span_id_returns_none(self, pipeline):
        """Test that missing span_id causes the row to be skipped.

        Span ID is a required field. Without it, the span cannot be
        properly identified and should be rejected.
        """
        row = {
            'trace_id': 'abc123',
            'span_id': None,  # Missing span_id
            'timestamp_micros': 1700000000000000,
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_convert_row_to_span_empty_span_id_returns_none(self, pipeline):
        """Test that empty string span_id causes the row to be skipped."""
        row = {
            'trace_id': 'abc123',
            'span_id': '',  # Empty span_id
            'timestamp_micros': 1700000000000000,
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_convert_row_to_span_missing_trace_id_returns_none(self, pipeline):
        """Test that missing trace_id causes the row to be skipped.

        Trace ID is a required field for correlating spans within
        a distributed trace.
        """
        row = {
            'trace_id': None,  # Missing trace_id
            'span_id': 'span-001',
            'timestamp_micros': 1700000000000000,
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_convert_row_to_span_empty_trace_id_returns_none(self, pipeline):
        """Test that empty string trace_id causes the row to be skipped."""
        row = {
            'trace_id': '',  # Empty trace_id
            'span_id': 'span-001',
            'timestamp_micros': 1700000000000000,
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_convert_row_to_span_negative_duration_returns_none(self, pipeline):
        """Test that negative duration causes the row to be skipped.

        Negative durations are invalid and indicate corrupted data.
        The implementation skips such spans rather than coercing to 0.
        """
        row = {
            'trace_id': 'abc123',
            'span_id': 'span-001',
            'timestamp_micros': 1700000000000000,
            'duration_micros': -100,  # Negative duration
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_convert_row_to_span_zero_duration_is_valid(self, pipeline):
        """Test that zero duration is accepted as valid.

        Zero duration spans can occur for instantaneous events.
        """
        row = {
            'trace_id': 'abc123',
            'span_id': 'span-001',
            'timestamp_micros': 1700000000000000,
            'duration_micros': 0,  # Zero duration
        }

        span = pipeline._convert_row_to_span(row)
        assert span is not None
        assert span['Duration'] == 0

    def test_convert_row_to_span_missing_parent_id_uses_empty_string(self, pipeline):
        """Test that missing parent_id is converted to empty string.

        Root spans do not have a parent, so None should become empty string.
        """
        row = {
            'trace_id': 'abc123',
            'span_id': 'span-001',
            'parent_id': None,  # No parent (root span)
            'timestamp_micros': 1700000000000000,
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is not None
        assert span['ParentID'] == ''

    def test_convert_row_to_span_invalid_start_time_returns_none(self, pipeline):
        """Test that invalid start time causes the row to be skipped."""
        row = {
            'trace_id': 'abc123',
            'span_id': 'span-001',
            'timestamp_micros': 0,  # Invalid (zero) start time
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_convert_row_to_span_negative_start_time_returns_none(self, pipeline):
        """Test that negative start time causes the row to be skipped."""
        row = {
            'trace_id': 'abc123',
            'span_id': 'span-001',
            'timestamp_micros': -1,  # Negative start time
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_convert_row_to_span_non_integer_duration_returns_none(self, pipeline):
        """Test that non-integer duration that cannot be converted returns None."""
        row = {
            'trace_id': 'abc123',
            'span_id': 'span-001',
            'timestamp_micros': 1700000000000000,
            'duration_micros': 'not-a-number',  # Invalid duration type
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None


class TestTagParsing:
    """Tests for tag extraction from parquet rows."""

    def test_extract_tags_direct_field_access(self, pipeline):
        """Test extraction of tags from direct field names."""
        row = {
            'http.method': 'POST',
            'http.url': '/api/data',
            'http.status_code': '201',
        }

        tags = pipeline._extract_tags(row)

        assert tags['http.method'] == 'POST'
        assert tags['http.url'] == '/api/data'
        assert tags['http.status_code'] == '201'

    def test_extract_tags_underscore_field_names(self, pipeline):
        """Test extraction of tags from underscore-separated field names.

        Some parquet files use underscores instead of dots in field names.
        """
        row = {
            'http_method': 'PUT',
            'http_status_code': '200',
        }

        tags = pipeline._extract_tags(row)

        assert tags['http.method'] == 'PUT'
        assert tags['http.status_code'] == '200'

    def test_extract_tags_from_attributes_map(self, pipeline):
        """Test extraction of tags from nested attributes dictionary."""
        row = {
            'attributes': {
                'custom.tag': 'custom-value',
                'another.tag': 'another-value',
            }
        }

        tags = pipeline._extract_tags(row)

        assert tags['custom.tag'] == 'custom-value'
        assert tags['another.tag'] == 'another-value'

    def test_extract_tags_identity_fields(self, pipeline):
        """Test extraction of service identity fields."""
        row = {
            'service_identity': 'my-service',
            'env': 'production',
            'feature_group': 'payments',
            'feature_name': 'checkout',
        }

        tags = pipeline._extract_tags(row)

        assert tags['service_identity'] == 'my-service'
        assert tags['env'] == 'production'
        assert tags['feature_group'] == 'payments'
        assert tags['feature_name'] == 'checkout'

    def test_extract_tags_skips_none_values(self, pipeline):
        """Test that None values are not included in tags."""
        row = {
            'http.method': 'GET',
            'http.url': None,  # Should be skipped
        }

        tags = pipeline._extract_tags(row)

        assert 'http.method' in tags
        assert 'http.url' not in tags

    def test_extract_tags_skips_empty_identity_fields(self, pipeline):
        """Test that empty string identity fields are not included."""
        row = {
            'service_identity': 'my-service',
            'env': '',  # Empty, should be skipped
            'feature_group': None,  # None, should be skipped
        }

        tags = pipeline._extract_tags(row)

        assert 'service_identity' in tags
        assert 'env' not in tags
        assert 'feature_group' not in tags

    def test_extract_tags_converts_values_to_strings(self, pipeline):
        """Test that all tag values are converted to strings."""
        row = {
            'http.status_code': 200,  # Integer, not string
            'attributes': {
                'count': 42,
                'enabled': True,
            }
        }

        tags = pipeline._extract_tags(row)

        assert tags['http.status_code'] == '200'
        assert tags['count'] == '42'
        assert tags['enabled'] == 'True'

    def test_extract_tags_handles_missing_attributes(self, pipeline):
        """Test that missing attributes field doesn't cause errors."""
        row = {
            'http.method': 'GET',
            # No 'attributes' field
        }

        tags = pipeline._extract_tags(row)
        assert tags['http.method'] == 'GET'

    def test_extract_tags_handles_non_dict_attributes(self, pipeline):
        """Test that non-dictionary attributes field is handled gracefully."""
        row = {
            'http.method': 'GET',
            'attributes': 'not-a-dict',  # Invalid type
        }

        tags = pipeline._extract_tags(row)
        # Should not raise, should just skip attributes
        assert tags['http.method'] == 'GET'


# =============================================================================
# Validation Tests
# =============================================================================


class TestSpanValidation:
    """Tests for span validation logic within _convert_row_to_span."""

    def test_validate_span_with_all_required_fields(self, pipeline, valid_row):
        """Test that a span with all required fields passes validation."""
        span = pipeline._convert_row_to_span(valid_row)
        assert span is not None
        assert 'TraceID' in span
        assert 'SpanID' in span
        assert 'StartTime' in span
        assert 'Duration' in span

    def test_validate_span_missing_required_trace_id(self, pipeline):
        """Test validation fails when trace_id is missing."""
        row = {
            'span_id': 'span-001',
            'timestamp_micros': 1700000000000000,
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_validate_span_missing_required_span_id(self, pipeline):
        """Test validation fails when span_id is missing."""
        row = {
            'trace_id': 'trace-001',
            'timestamp_micros': 1700000000000000,
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_validate_span_missing_timestamp(self, pipeline):
        """Test validation fails when timestamp is missing."""
        row = {
            'trace_id': 'trace-001',
            'span_id': 'span-001',
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_validate_span_missing_duration(self, pipeline):
        """Test validation fails when duration is missing."""
        row = {
            'trace_id': 'trace-001',
            'span_id': 'span-001',
            'timestamp_micros': 1700000000000000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_validate_span_invalid_duration_type(self, pipeline):
        """Test validation fails with invalid duration type."""
        row = {
            'trace_id': 'trace-001',
            'span_id': 'span-001',
            'timestamp_micros': 1700000000000000,
            'duration_micros': 'invalid',
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_validate_span_with_empty_strings(self, pipeline):
        """Test validation fails when required fields are empty strings."""
        row = {
            'trace_id': '',
            'span_id': '',
            'timestamp_micros': 1700000000000000,
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is None

    def test_validate_span_allows_optional_fields_to_be_missing(self, pipeline):
        """Test that optional fields can be missing without failing validation."""
        row = {
            'trace_id': 'trace-001',
            'span_id': 'span-001',
            'timestamp_micros': 1700000000000000,
            'duration_micros': 150000,
            # No parent_id, name, or service_name - all optional
        }

        span = pipeline._convert_row_to_span(row)
        assert span is not None
        assert span['ParentID'] == ''
        assert span['OperationName'] == ''
        assert span['ServiceName'] == ''


# =============================================================================
# Batch Sending Tests
# =============================================================================


class TestBatchSending:
    """Tests for batch sending functionality."""

    def test_send_batch_success(self, pipeline):
        """Test successful batch send with 202 response."""
        mock_response = MagicMock()
        mock_response.status_code = 202
        mock_response.json.return_value = {'accepted': 10, 'rejected': 0}
        pipeline.session.post.return_value = mock_response

        spans = [{'TraceID': f'trace-{i}', 'SpanID': f'span-{i}'} for i in range(10)]
        result = pipeline._send_batch(spans)

        assert result is True
        assert pipeline.metrics.spans_sent == 10
        assert pipeline.metrics.batches_sent == 1
        pipeline.session.post.assert_called_once()
        call_args = pipeline.session.post.call_args
        assert '/api/spans/batch' in call_args[0][0]
        assert call_args[1]['json'] == spans

    def test_send_batch_empty_list(self, pipeline):
        """Test that empty batch returns True without making request."""
        result = pipeline._send_batch([])

        assert result is True
        pipeline.session.post.assert_not_called()

    def test_send_batch_failure_returns_false(self, pipeline):
        """Test that non-202 response returns False."""
        mock_response = MagicMock()
        mock_response.status_code = 500
        mock_response.text = 'Internal Server Error'
        pipeline.session.post.return_value = mock_response

        spans = [{'TraceID': 'trace-1', 'SpanID': 'span-1'}]
        result = pipeline._send_batch(spans)

        assert result is False

    def test_send_batch_records_response_time(self, pipeline):
        """Test that response time is recorded for successful requests."""
        mock_response = MagicMock()
        mock_response.status_code = 202
        mock_response.json.return_value = {'accepted': 1, 'rejected': 0}
        pipeline.session.post.return_value = mock_response

        spans = [{'TraceID': 'trace-1', 'SpanID': 'span-1'}]
        pipeline._send_batch(spans)

        # Response time should be recorded
        assert pipeline.metrics.avg_response_time_ms > 0

    def test_send_with_retry_succeeds_on_first_attempt(self, pipeline):
        """Test retry logic succeeds immediately when first attempt works."""
        mock_response = MagicMock()
        mock_response.status_code = 202
        mock_response.json.return_value = {'accepted': 5, 'rejected': 0}
        pipeline.session.post.return_value = mock_response

        spans = [{'TraceID': f'trace-{i}'} for i in range(5)]
        result = pipeline._send_with_retry(spans)

        assert result is True
        assert pipeline.session.post.call_count == 1

    def test_send_with_retry_retries_on_timeout(self, pipeline):
        """Test retry logic retries on request timeout."""
        import requests

        # First two calls timeout, third succeeds
        mock_response = MagicMock()
        mock_response.status_code = 202
        mock_response.json.return_value = {'accepted': 1, 'rejected': 0}

        pipeline.session.post.side_effect = [
            requests.exceptions.Timeout(),
            requests.exceptions.Timeout(),
            mock_response,
        ]

        spans = [{'TraceID': 'trace-1'}]

        with patch('time.sleep'):  # Skip actual delays
            result = pipeline._send_with_retry(spans)

        assert result is True
        assert pipeline.session.post.call_count == 3

    def test_send_with_retry_retries_on_connection_error(self, pipeline):
        """Test retry logic retries on connection error."""
        import requests

        mock_response = MagicMock()
        mock_response.status_code = 202
        mock_response.json.return_value = {'accepted': 1, 'rejected': 0}

        pipeline.session.post.side_effect = [
            requests.exceptions.ConnectionError("Connection refused"),
            mock_response,
        ]

        spans = [{'TraceID': 'trace-1'}]

        with patch('time.sleep'):
            result = pipeline._send_with_retry(spans)

        assert result is True
        assert pipeline.session.post.call_count == 2

    def test_send_with_retry_fails_after_max_retries(self, pipeline):
        """Test retry logic fails after exhausting all retries."""
        import requests

        pipeline.session.post.side_effect = requests.exceptions.Timeout()

        spans = [{'TraceID': 'trace-1'}]

        with patch('time.sleep'):
            result = pipeline._send_with_retry(spans)

        assert result is False
        assert pipeline.session.post.call_count == pipeline.max_retries
        assert pipeline.metrics.batches_failed == 1
        assert pipeline.metrics.spans_failed == 1

    def test_send_with_retry_exponential_backoff(self, pipeline):
        """Test that retry delays increase exponentially."""
        import requests

        mock_response = MagicMock()
        mock_response.status_code = 202
        mock_response.json.return_value = {'accepted': 1, 'rejected': 0}

        pipeline.session.post.side_effect = [
            requests.exceptions.Timeout(),
            requests.exceptions.Timeout(),
            mock_response,
        ]

        spans = [{'TraceID': 'trace-1'}]
        sleep_calls = []

        with patch('time.sleep', side_effect=lambda x: sleep_calls.append(x)):
            pipeline._send_with_retry(spans)

        # Exponential backoff: 2^0=1, 2^1=2
        assert sleep_calls == [1, 2]


class TestBackpressureHandling:
    """Tests for backpressure handling in batch sending."""

    def test_backpressure_increases_delay_on_slow_response(self, pipeline):
        """Test that delay increases when response time exceeds threshold."""
        mock_response = MagicMock()
        mock_response.status_code = 202
        mock_response.json.return_value = {'accepted': 1, 'rejected': 0}
        pipeline.session.post.return_value = mock_response

        # Record many slow response times so average stays high even after
        # _send_batch records one more (fast) response time.
        # With 99 samples at 1500ms and 1 fast sample, average stays well above threshold.
        for _ in range(99):
            pipeline.metrics.record_response_time(1500.0)  # Above default 1000ms threshold

        spans = [{'TraceID': 'trace-1'}]

        with patch('time.sleep'):
            pipeline._send_batch_with_backpressure(spans)

        # Delay should have increased
        assert pipeline._current_delay > 0

    def test_backpressure_decreases_delay_on_fast_response(self, pipeline):
        """Test that delay decreases when response time is well below threshold."""
        mock_response = MagicMock()
        mock_response.status_code = 202
        mock_response.json.return_value = {'accepted': 1, 'rejected': 0}
        pipeline.session.post.return_value = mock_response

        # Set initial delay
        pipeline._current_delay = 2.0
        # Simulate fast response (below 50% of threshold)
        pipeline.metrics.avg_response_time_ms = 400  # Below 500ms (50% of 1000ms)

        spans = [{'TraceID': 'trace-1'}]

        with patch('time.sleep'):
            pipeline._send_batch_with_backpressure(spans)

        # Delay should have decreased
        assert pipeline._current_delay < 2.0

    def test_backpressure_applies_delay_before_sending(self, pipeline):
        """Test that backpressure delay is applied before sending batch."""
        mock_response = MagicMock()
        mock_response.status_code = 202
        mock_response.json.return_value = {'accepted': 1, 'rejected': 0}
        pipeline.session.post.return_value = mock_response

        pipeline._current_delay = 1.5
        pipeline.metrics.avg_response_time_ms = 500

        spans = [{'TraceID': 'trace-1'}]
        sleep_times = []

        with patch('time.sleep', side_effect=lambda x: sleep_times.append(x)):
            pipeline._send_batch_with_backpressure(spans)

        # Should have slept for the backpressure delay
        assert 1.5 in sleep_times

    def test_backpressure_delay_is_capped(self, pipeline):
        """Test that backpressure delay doesn't exceed maximum."""
        mock_response = MagicMock()
        mock_response.status_code = 202
        mock_response.json.return_value = {'accepted': 1, 'rejected': 0}
        pipeline.session.post.return_value = mock_response

        # Set delay near maximum
        pipeline._current_delay = 4.8
        pipeline.metrics.avg_response_time_ms = 2000  # Very slow

        spans = [{'TraceID': 'trace-1'}]

        with patch('time.sleep'):
            pipeline._send_batch_with_backpressure(spans)

        # Delay should be capped at 5.0
        assert pipeline._current_delay <= 5.0


class TestConnectionPooling:
    """Tests for connection pooling configuration."""

    def test_session_uses_http_adapter(self, mock_boto3):
        """Test that the session is configured with connection pooling adapter."""
        with patch.object(pipeline_module.requests, 'Session') as mock_session_class:
            mock_session = MagicMock()
            mock_session_class.return_value = mock_session

            with patch.object(pipeline_module.requests.adapters, 'HTTPAdapter') as mock_adapter_class:
                mock_adapter = MagicMock()
                mock_adapter_class.return_value = mock_adapter

                pipe = ContinuousPipeline(
                    s3_bucket='test-bucket',
                    analyzer_url='http://localhost:8080',
                )

                # Verify HTTPAdapter was created with pooling config
                mock_adapter_class.assert_called_once_with(
                    pool_connections=10,
                    pool_maxsize=20,
                    max_retries=0
                )

                # Verify adapter was mounted for both http and https
                mount_calls = mock_session.mount.call_args_list
                assert len(mount_calls) == 2
                assert any('http://' in str(call) for call in mount_calls)
                assert any('https://' in str(call) for call in mount_calls)

    def test_session_sends_json_content_type(self, pipeline):
        """Test that requests include correct Content-Type header."""
        mock_response = MagicMock()
        mock_response.status_code = 202
        mock_response.json.return_value = {'accepted': 1, 'rejected': 0}
        pipeline.session.post.return_value = mock_response

        spans = [{'TraceID': 'trace-1'}]
        pipeline._send_batch(spans)

        call_kwargs = pipeline.session.post.call_args[1]
        assert call_kwargs['headers']['Content-Type'] == 'application/json'


# =============================================================================
# Memory Management Tests
# =============================================================================


class TestMemoryManagement:
    """Tests for memory management and cleanup functionality."""

    def test_cleanup_old_entries_bounds_processed_files_set(self, pipeline):
        """Test that processed_files set is bounded by cleanup."""
        # Add more files than the limit
        limit = ContinuousPipeline.DEFAULT_MAX_PROCESSED_FILES
        for i in range(limit + 500):
            pipeline.processed_files.add(f'file-{i:05d}.parquet')

        assert len(pipeline.processed_files) > limit

        pipeline._cleanup_old_entries()

        # Should be reduced to half the limit
        expected_size = limit // 2
        assert len(pipeline.processed_files) == expected_size

    def test_cleanup_old_entries_keeps_most_recent_files(self, pipeline):
        """Test that cleanup keeps the most recent files (alphabetically last)."""
        # Add files with sortable names
        for i in range(1500):
            pipeline.processed_files.add(f'2024-01-{i:04d}.parquet')

        pipeline._cleanup_old_entries()

        # Should keep the highest numbered files (most recent by naming convention)
        remaining = sorted(pipeline.processed_files)
        # Verify the kept files are the "latest" ones
        assert '2024-01-1499.parquet' in pipeline.processed_files
        assert '2024-01-0000.parquet' not in pipeline.processed_files

    def test_cleanup_old_entries_bounds_processed_timestamps_set(self, pipeline):
        """Test that processed_timestamps set is bounded by cleanup."""
        limit = ContinuousPipeline.DEFAULT_MAX_PROCESSED_TIMESTAMPS
        for i in range(limit + 500):
            pipeline.processed_timestamps.add(1700000000 + i)

        assert len(pipeline.processed_timestamps) > limit

        pipeline._cleanup_old_entries()

        expected_size = limit // 2
        assert len(pipeline.processed_timestamps) == expected_size

    def test_cleanup_does_nothing_when_under_limit(self, pipeline):
        """Test that cleanup doesn't modify sets when under the limit."""
        # Add fewer files than the limit
        original_count = 100
        for i in range(original_count):
            pipeline.processed_files.add(f'file-{i}.parquet')

        pipeline._cleanup_old_entries()

        assert len(pipeline.processed_files) == original_count

    def test_thread_safe_access_to_processed_files(self, pipeline):
        """Test that processed_files can be safely accessed from multiple threads."""
        errors = []
        iterations = 100

        def add_files():
            try:
                for i in range(iterations):
                    with pipeline._files_lock:
                        pipeline.processed_files.add(f'thread-add-{threading.current_thread().name}-{i}')
            except Exception as e:
                errors.append(e)

        def read_files():
            try:
                for _ in range(iterations):
                    with pipeline._files_lock:
                        _ = len(pipeline.processed_files)
                        _ = f'test-file' in pipeline.processed_files
            except Exception as e:
                errors.append(e)

        def cleanup_files():
            try:
                for _ in range(10):
                    pipeline._cleanup_old_entries()
            except Exception as e:
                errors.append(e)

        threads = [
            threading.Thread(target=add_files, name='adder-1'),
            threading.Thread(target=add_files, name='adder-2'),
            threading.Thread(target=read_files, name='reader-1'),
            threading.Thread(target=read_files, name='reader-2'),
            threading.Thread(target=cleanup_files, name='cleaner'),
        ]

        for t in threads:
            t.start()

        for t in threads:
            t.join()

        assert len(errors) == 0, f"Thread safety errors: {errors}"

    def test_processed_files_is_a_set(self, pipeline):
        """Test that processed_files uses a set to avoid duplicates."""
        pipeline.processed_files.add('file-1.parquet')
        pipeline.processed_files.add('file-1.parquet')
        pipeline.processed_files.add('file-1.parquet')

        assert len(pipeline.processed_files) == 1


# =============================================================================
# Metrics Tests
# =============================================================================


class TestMetrics:
    """Tests for the PipelineMetrics class."""

    def test_metrics_initial_values(self, metrics):
        """Test that metrics start with zero values."""
        assert metrics.spans_sent == 0
        assert metrics.spans_failed == 0
        assert metrics.spans_skipped == 0
        assert metrics.batches_sent == 0
        assert metrics.batches_failed == 0
        assert metrics.files_processed == 0
        assert metrics.files_failed == 0
        assert metrics.total_bytes_processed == 0
        assert metrics.avg_response_time_ms == 0.0

    def test_increment_sent(self, metrics):
        """Test incrementing sent spans count."""
        metrics.increment_sent(10)
        assert metrics.spans_sent == 10

        metrics.increment_sent(5)
        assert metrics.spans_sent == 15

    def test_increment_sent_default_value(self, metrics):
        """Test incrementing sent with default value of 1."""
        metrics.increment_sent()
        assert metrics.spans_sent == 1

    def test_increment_failed(self, metrics):
        """Test incrementing failed spans count."""
        metrics.increment_failed(3)
        assert metrics.spans_failed == 3

    def test_increment_skipped(self, metrics):
        """Test incrementing skipped spans count."""
        metrics.increment_skipped(7)
        assert metrics.spans_skipped == 7

    def test_increment_batches_sent(self, metrics):
        """Test incrementing sent batches count."""
        metrics.increment_batches_sent()
        metrics.increment_batches_sent()
        assert metrics.batches_sent == 2

    def test_increment_batches_failed(self, metrics):
        """Test incrementing failed batches count."""
        metrics.increment_batches_failed()
        assert metrics.batches_failed == 1

    def test_increment_files_processed(self, metrics):
        """Test incrementing processed files count."""
        metrics.increment_files_processed()
        metrics.increment_files_processed()
        metrics.increment_files_processed()
        assert metrics.files_processed == 3

    def test_increment_files_failed(self, metrics):
        """Test incrementing failed files count."""
        metrics.increment_files_failed()
        assert metrics.files_failed == 1

    def test_add_bytes_processed(self, metrics):
        """Test adding to bytes processed."""
        metrics.add_bytes_processed(1024)
        metrics.add_bytes_processed(2048)
        assert metrics.total_bytes_processed == 3072

    def test_record_response_time_calculates_average(self, metrics):
        """Test that response time recording calculates correct average."""
        metrics.record_response_time(100.0)
        assert metrics.avg_response_time_ms == 100.0

        metrics.record_response_time(200.0)
        assert metrics.avg_response_time_ms == 150.0

        metrics.record_response_time(300.0)
        assert metrics.avg_response_time_ms == 200.0

    def test_record_response_time_keeps_last_100_samples(self, metrics):
        """Test that only the last 100 response times are kept."""
        # Add 150 samples
        for i in range(150):
            metrics.record_response_time(float(i))

        # Should only have 100 samples
        assert len(metrics._response_times) == 100

        # Average should be of last 100 (50-149)
        expected_avg = sum(range(50, 150)) / 100
        assert metrics.avg_response_time_ms == expected_avg

    def test_get_summary_returns_correct_format(self, metrics):
        """Test that get_summary returns all expected fields."""
        metrics.increment_sent(100)
        metrics.increment_failed(5)
        metrics.increment_skipped(10)
        metrics.increment_batches_sent()
        metrics.increment_batches_sent()
        metrics.increment_batches_failed()
        metrics.increment_files_processed()
        metrics.increment_files_failed()
        metrics.add_bytes_processed(1024 * 1024)  # 1 MB
        metrics.record_response_time(150.0)

        summary = metrics.get_summary()

        assert summary['spans_sent'] == 100
        assert summary['spans_failed'] == 5
        assert summary['spans_skipped'] == 10
        assert summary['batches_sent'] == 2
        assert summary['batches_failed'] == 1
        assert summary['files_processed'] == 1
        assert summary['files_failed'] == 1
        assert summary['total_bytes_mb'] == 1.0
        assert summary['avg_response_time_ms'] == 150.0

    def test_metrics_thread_safety(self, metrics):
        """Test that metrics can be updated safely from multiple threads."""
        errors = []
        iterations = 1000

        def increment_sent():
            try:
                for _ in range(iterations):
                    metrics.increment_sent()
            except Exception as e:
                errors.append(e)

        def increment_failed():
            try:
                for _ in range(iterations):
                    metrics.increment_failed()
            except Exception as e:
                errors.append(e)

        def record_times():
            try:
                for i in range(iterations):
                    metrics.record_response_time(float(i))
            except Exception as e:
                errors.append(e)

        def get_summaries():
            try:
                for _ in range(iterations):
                    _ = metrics.get_summary()
            except Exception as e:
                errors.append(e)

        threads = [
            threading.Thread(target=increment_sent),
            threading.Thread(target=increment_sent),
            threading.Thread(target=increment_failed),
            threading.Thread(target=record_times),
            threading.Thread(target=get_summaries),
        ]

        for t in threads:
            t.start()

        for t in threads:
            t.join()

        assert len(errors) == 0, f"Thread safety errors: {errors}"
        assert metrics.spans_sent == 2 * iterations
        assert metrics.spans_failed == iterations


class TestMetricsStringFormatting:
    """Tests for metrics summary string formatting."""

    def test_summary_bytes_formatted_as_mb(self, metrics):
        """Test that bytes are formatted as megabytes in summary."""
        metrics.add_bytes_processed(5 * 1024 * 1024)  # 5 MB
        summary = metrics.get_summary()
        assert summary['total_bytes_mb'] == 5.0

    def test_summary_bytes_partial_mb(self, metrics):
        """Test that partial megabytes are correctly calculated."""
        metrics.add_bytes_processed(512 * 1024)  # 0.5 MB
        summary = metrics.get_summary()
        assert summary['total_bytes_mb'] == 0.5

    def test_summary_zero_bytes(self, metrics):
        """Test that zero bytes is formatted correctly."""
        summary = metrics.get_summary()
        assert summary['total_bytes_mb'] == 0.0

    def test_summary_avg_response_time_precision(self, metrics):
        """Test that average response time maintains precision."""
        metrics.record_response_time(33.333)
        metrics.record_response_time(66.666)
        summary = metrics.get_summary()
        # Average should be approximately 49.9995
        assert abs(summary['avg_response_time_ms'] - 49.9995) < 0.001


# =============================================================================
# Integration-style Tests
# =============================================================================


class TestPipelineIntegration:
    """Integration-style tests for the complete pipeline flow."""

    def test_pipeline_initialization(self, mock_boto3, mock_requests_session):
        """Test that pipeline initializes with correct configuration."""
        pipe = ContinuousPipeline(
            s3_bucket='my-bucket',
            analyzer_url='http://analyzer:8080/',  # Note trailing slash
            s3_prefix='data/',
            batch_size=200,
            poll_interval=60,
            max_retries=5,
        )

        assert pipe.s3_bucket == 'my-bucket'
        assert pipe.analyzer_url == 'http://analyzer:8080'  # Trailing slash removed
        assert pipe.s3_prefix == 'data/'
        assert pipe.batch_size == 200
        assert pipe.poll_interval == 60
        assert pipe.max_retries == 5

    def test_pipeline_get_status(self, pipeline):
        """Test that get_status returns complete pipeline state."""
        # Modify some state
        pipeline.processed_files.add('file1.parquet')
        pipeline.processed_files.add('file2.parquet')
        pipeline._current_delay = 1.5
        pipeline._running = True

        status = pipeline.get_status()

        assert status['running'] is True
        assert status['s3_bucket'] == 'test-bucket'
        assert status['s3_prefix'] == 'spans/'
        assert status['analyzer_url'] == 'http://localhost:8080'
        assert status['batch_size'] == 100
        assert status['backpressure_delay'] == 1.5
        assert status['processed_files_count'] == 2
        assert 'metrics' in status

    def test_to_int_helper_with_valid_values(self, pipeline):
        """Test the _to_int helper method with valid inputs."""
        assert pipeline._to_int(42) == 42
        assert pipeline._to_int('123') == 123
        assert pipeline._to_int(0) == 0
        assert pipeline._to_int(-5) == -5

    def test_to_int_helper_with_invalid_values(self, pipeline):
        """Test the _to_int helper method with invalid inputs."""
        assert pipeline._to_int(None) is None
        assert pipeline._to_int('not-a-number') is None
        assert pipeline._to_int([1, 2, 3]) is None
        assert pipeline._to_int({'key': 'value'}) is None

    def test_to_int_helper_with_float(self, pipeline):
        """Test the _to_int helper method truncates floats."""
        assert pipeline._to_int(3.7) == 3
        assert pipeline._to_int(3.2) == 3


# =============================================================================
# Edge Cases and Error Handling
# =============================================================================


class TestEdgeCases:
    """Tests for edge cases and error handling."""

    def test_convert_row_handles_none_values_gracefully(self, pipeline):
        """Test that conversion handles None values in various fields."""
        row = {
            'trace_id': 'trace-1',
            'span_id': 'span-1',
            'parent_id': None,
            'name': None,
            'timestamp_micros': 1700000000000000,
            'duration_micros': 150000,
            'local_endpoint_service_name': None,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is not None
        assert span['ParentID'] == ''
        assert span['OperationName'] == ''
        assert span['ServiceName'] == ''

    def test_convert_row_with_unicode_values(self, pipeline):
        """Test that conversion handles unicode strings correctly."""
        row = {
            'trace_id': 'trace-\u00e9\u00e8\u00ea',  # French accents
            'span_id': 'span-\u4e2d\u6587',  # Chinese characters
            'name': '\ud83d\ude00 Emoji Service',
            'timestamp_micros': 1700000000000000,
            'duration_micros': 150000,
        }

        span = pipeline._convert_row_to_span(row)
        assert span is not None
        assert '\u00e9' in span['TraceID']
        assert '\u4e2d' in span['SpanID']

    def test_convert_row_with_very_large_duration(self, pipeline):
        """Test that very large durations are handled correctly."""
        row = {
            'trace_id': 'trace-1',
            'span_id': 'span-1',
            'timestamp_micros': 1700000000000000,
            'duration_micros': 2**62,  # Very large number
        }

        span = pipeline._convert_row_to_span(row)
        assert span is not None
        assert span['Duration'] == 2**62

    def test_send_batch_handles_request_exception(self, pipeline):
        """Test that request exceptions are properly propagated."""
        import requests

        pipeline.session.post.side_effect = requests.exceptions.RequestException("Network error")

        spans = [{'TraceID': 'trace-1'}]

        with pytest.raises(requests.exceptions.RequestException):
            pipeline._send_batch(spans)

    def test_extract_tags_with_deeply_nested_attributes(self, pipeline):
        """Test tag extraction doesn't fail with complex attribute structures."""
        row = {
            'attributes': {
                'simple': 'value',
                'nested': {'inner': 'value'},  # Nested dict gets stringified
                'list': [1, 2, 3],  # List gets stringified
            }
        }

        # Should not raise an exception
        tags = pipeline._extract_tags(row)
        assert 'simple' in tags


if __name__ == '__main__':
    pytest.main([__file__, '-v'])
