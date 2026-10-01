#!/usr/bin/env python3
"""
Continuous Pipeline for Trace Analyzer

This script continuously monitors an S3 bucket for new parquet span files,
downloads them, processes the spans, and sends them to the trace analyzer
service in batches.

Features:
- Batch processing with configurable batch size
- Connection pooling for efficient HTTP requests
- Retry logic with exponential backoff
- Thread-safe file tracking
- Memory-bounded state management
- Backpressure handling based on response times
- Comprehensive metrics tracking
- Graceful shutdown handling

Usage:
    python continuous-pipeline.py --s3-bucket my-bucket --analyzer-url http://localhost:8080

Environment Variables:
    AWS_ACCESS_KEY_ID: AWS access key
    AWS_SECRET_ACCESS_KEY: AWS secret key
    AWS_DEFAULT_REGION: AWS region (default: us-east-1)
    ANALYZER_URL: URL of the trace analyzer service
    S3_BUCKET: S3 bucket containing parquet files
    S3_PREFIX: S3 prefix to filter files (optional)
    BATCH_SIZE: Number of spans per batch (default: 500)
    POLL_INTERVAL: Seconds between S3 polls (default: 30)
"""

import argparse
import atexit
import logging
import os
import signal
import sys
import tempfile
import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Any, Dict, List, Optional, Set

import boto3
import pyarrow.parquet as pq
import requests
from botocore.exceptions import ClientError

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
    handlers=[
        logging.StreamHandler(sys.stdout)
    ]
)
logger = logging.getLogger(__name__)


@dataclass
class PipelineMetrics:
    """Tracks pipeline processing metrics."""

    spans_sent: int = 0
    spans_failed: int = 0
    spans_skipped: int = 0
    batches_sent: int = 0
    batches_failed: int = 0
    files_processed: int = 0
    files_failed: int = 0
    total_bytes_processed: int = 0
    avg_response_time_ms: float = 0.0
    _response_times: List[float] = field(default_factory=list)
    _lock: threading.Lock = field(default_factory=threading.Lock)

    def record_response_time(self, response_time_ms: float) -> None:
        """Record a response time and update the rolling average."""
        with self._lock:
            self._response_times.append(response_time_ms)
            # Keep only last 100 samples
            if len(self._response_times) > 100:
                self._response_times = self._response_times[-100:]
            self.avg_response_time_ms = sum(self._response_times) / len(self._response_times)

    def increment_sent(self, count: int = 1) -> None:
        """Thread-safe increment of sent spans."""
        with self._lock:
            self.spans_sent += count

    def increment_failed(self, count: int = 1) -> None:
        """Thread-safe increment of failed spans."""
        with self._lock:
            self.spans_failed += count

    def increment_skipped(self, count: int = 1) -> None:
        """Thread-safe increment of skipped spans."""
        with self._lock:
            self.spans_skipped += count

    def increment_batches_sent(self) -> None:
        """Thread-safe increment of sent batches."""
        with self._lock:
            self.batches_sent += 1

    def increment_batches_failed(self) -> None:
        """Thread-safe increment of failed batches."""
        with self._lock:
            self.batches_failed += 1

    def increment_files_processed(self) -> None:
        """Thread-safe increment of processed files."""
        with self._lock:
            self.files_processed += 1

    def increment_files_failed(self) -> None:
        """Thread-safe increment of failed files."""
        with self._lock:
            self.files_failed += 1

    def add_bytes_processed(self, byte_count: int) -> None:
        """Thread-safe addition to bytes processed."""
        with self._lock:
            self.total_bytes_processed += byte_count

    def get_summary(self) -> Dict[str, Any]:
        """Get a summary of current metrics."""
        with self._lock:
            return {
                'spans_sent': self.spans_sent,
                'spans_failed': self.spans_failed,
                'spans_skipped': self.spans_skipped,
                'batches_sent': self.batches_sent,
                'batches_failed': self.batches_failed,
                'files_processed': self.files_processed,
                'files_failed': self.files_failed,
                'total_bytes_mb': self.total_bytes_processed / (1024 * 1024),
                'avg_response_time_ms': self.avg_response_time_ms,
            }


class ContinuousPipeline:
    """
    Continuous pipeline for processing parquet span files from S3 and sending
    them to the trace analyzer service.

    This class handles:
    - S3 file discovery and download
    - Parquet file parsing with pyarrow
    - Batch processing and sending
    - Retry logic with exponential backoff
    - Thread-safe state management
    - Graceful shutdown
    """

    # Default configuration
    DEFAULT_BATCH_SIZE = 500
    DEFAULT_POLL_INTERVAL = 30
    DEFAULT_MAX_RETRIES = 3
    DEFAULT_REQUEST_TIMEOUT = 30
    DEFAULT_BACKPRESSURE_THRESHOLD_MS = 1000
    DEFAULT_MAX_PROCESSED_FILES = 1000
    DEFAULT_MAX_PROCESSED_TIMESTAMPS = 1000

    def __init__(
        self,
        s3_bucket: str,
        analyzer_url: str,
        s3_prefix: str = '',
        batch_size: int = DEFAULT_BATCH_SIZE,
        poll_interval: int = DEFAULT_POLL_INTERVAL,
        max_retries: int = DEFAULT_MAX_RETRIES,
        request_timeout: int = DEFAULT_REQUEST_TIMEOUT,
        max_workers: int = 4,
        aws_access_key: Optional[str] = None,
        aws_secret_key: Optional[str] = None,
        aws_region: str = 'us-east-1',
    ):
        """
        Initialize the continuous pipeline.

        Args:
            s3_bucket: Name of the S3 bucket containing parquet files
            analyzer_url: Base URL of the trace analyzer service
            s3_prefix: Optional prefix to filter S3 objects
            batch_size: Number of spans to send per batch
            poll_interval: Seconds between S3 polling
            max_retries: Maximum retry attempts for failed requests
            request_timeout: Timeout in seconds for HTTP requests
            max_workers: Maximum number of worker threads
            aws_access_key: AWS access key (optional, uses env/profile if not provided)
            aws_secret_key: AWS secret key (optional, uses env/profile if not provided)
            aws_region: AWS region for S3
        """
        self.s3_bucket = s3_bucket
        self.analyzer_url = analyzer_url.rstrip('/')
        self.s3_prefix = s3_prefix
        self.batch_size = batch_size
        self.poll_interval = poll_interval
        self.max_retries = max_retries
        self.request_timeout = request_timeout
        self.max_workers = max_workers

        # Thread-safe state management
        self._files_lock = threading.Lock()
        self._timestamps_lock = threading.Lock()
        self.processed_files: Set[str] = set()
        self.processed_timestamps: Set[int] = set()

        # Running state
        self._running = False
        self._shutdown_event = threading.Event()

        # Metrics
        self.metrics = PipelineMetrics()

        # Backpressure state
        self._backpressure_threshold_ms = self.DEFAULT_BACKPRESSURE_THRESHOLD_MS
        self._current_delay = 0.0

        # Initialize S3 client
        session_kwargs = {}
        if aws_access_key and aws_secret_key:
            session_kwargs['aws_access_key_id'] = aws_access_key
            session_kwargs['aws_secret_access_key'] = aws_secret_key
        session_kwargs['region_name'] = aws_region

        self.s3_client = boto3.client('s3', **session_kwargs)

        # Initialize HTTP session with connection pooling
        self.session = requests.Session()
        adapter = requests.adapters.HTTPAdapter(
            pool_connections=10,
            pool_maxsize=20,
            max_retries=0  # We handle retries ourselves
        )
        self.session.mount('http://', adapter)
        self.session.mount('https://', adapter)

        # Thread pool for parallel processing
        self._executor: Optional[ThreadPoolExecutor] = None

        logger.info(
            f"Initialized ContinuousPipeline: bucket={s3_bucket}, "
            f"analyzer={analyzer_url}, batch_size={batch_size}"
        )

    def _setup_signal_handlers(self) -> None:
        """Set up signal handlers for graceful shutdown."""
        def signal_handler(signum, frame):
            sig_name = signal.Signals(signum).name
            logger.info(f"Received {sig_name}, initiating graceful shutdown...")
            self._running = False
            self._shutdown_event.set()

        signal.signal(signal.SIGINT, signal_handler)
        signal.signal(signal.SIGTERM, signal_handler)

    def start(self) -> None:
        """Start the continuous pipeline."""
        self._running = True
        self._shutdown_event.clear()
        self._setup_signal_handlers()

        # Initialize thread pool
        self._executor = ThreadPoolExecutor(max_workers=self.max_workers)

        # Register cleanup on exit
        atexit.register(self._cleanup)

        logger.info("Starting continuous pipeline...")

        try:
            self._run_loop()
        except Exception as e:
            logger.error(f"Pipeline error: {e}", exc_info=True)
        finally:
            self._cleanup()

    def _cleanup(self) -> None:
        """Clean up resources on shutdown."""
        logger.info("Cleaning up pipeline resources...")

        # Shutdown executor
        if self._executor:
            # cancel_futures was added in Python 3.9
            if sys.version_info >= (3, 9):
                self._executor.shutdown(wait=True, cancel_futures=True)
            else:
                self._executor.shutdown(wait=True)
            self._executor = None

        # Close HTTP session
        if self.session:
            self.session.close()

        # Log final metrics
        logger.info(f"Final metrics: {self.metrics.get_summary()}")

    def _run_loop(self) -> None:
        """Main processing loop."""
        while self._running:
            try:
                # List new files from S3
                new_files = self._list_new_files()

                if new_files:
                    logger.info(f"Found {len(new_files)} new files to process")
                    self._process_files(new_files)
                else:
                    logger.debug("No new files found")

                # Clean up old entries to bound memory
                self._cleanup_old_entries()

                # Log periodic metrics
                if self.metrics.files_processed % 10 == 0 and self.metrics.files_processed > 0:
                    logger.info(f"Metrics: {self.metrics.get_summary()}")

            except ClientError as e:
                logger.error(f"S3 error: {e}")
            except Exception as e:
                logger.error(f"Processing error: {e}", exc_info=True)

            # Wait for next poll interval or shutdown
            if self._shutdown_event.wait(timeout=self.poll_interval):
                break

    def _list_new_files(self) -> List[str]:
        """
        List parquet files from S3 that haven't been processed yet.

        Returns:
            List of S3 keys for new files
        """
        new_files = []

        paginator = self.s3_client.get_paginator('list_objects_v2')
        page_iterator = paginator.paginate(
            Bucket=self.s3_bucket,
            Prefix=self.s3_prefix
        )

        for page in page_iterator:
            if 'Contents' not in page:
                continue

            for obj in page['Contents']:
                key = obj['Key']

                # Only process parquet files
                if not key.endswith('.parquet'):
                    continue

                # Skip already processed files
                with self._files_lock:
                    if key in self.processed_files:
                        continue

                new_files.append(key)

        return new_files

    def _process_files(self, file_keys: List[str]) -> None:
        """
        Process multiple S3 files in parallel.

        Args:
            file_keys: List of S3 object keys to process
        """
        if not self._executor:
            return

        futures = {
            self._executor.submit(self._process_single_file, key): key
            for key in file_keys
        }

        for future in as_completed(futures):
            if not self._running:
                break

            key = futures[future]
            try:
                future.result()
                self.metrics.increment_files_processed()

                # Mark file as processed
                with self._files_lock:
                    self.processed_files.add(key)

            except Exception as e:
                logger.error(f"Failed to process file {key}: {e}")
                self.metrics.increment_files_failed()

    def _process_single_file(self, s3_key: str) -> None:
        """
        Download and process a single parquet file from S3.

        Args:
            s3_key: S3 object key for the parquet file
        """
        logger.info(f"Processing file: {s3_key}")

        # Download to temporary file
        with tempfile.NamedTemporaryFile(suffix='.parquet', delete=True) as tmp_file:
            tmp_path = Path(tmp_file.name)

            # Download from S3
            self.s3_client.download_file(self.s3_bucket, s3_key, str(tmp_path))

            # Track bytes processed
            file_size = tmp_path.stat().st_size
            self.metrics.add_bytes_processed(file_size)

            # Process the parquet file
            self._process_parquet_file(tmp_path)

    def _process_parquet_file(self, file_path: Path) -> None:
        """
        Process a parquet file using pyarrow for memory efficiency.

        Uses pyarrow's batch reading to process large files without
        loading everything into memory at once.

        Args:
            file_path: Path to the parquet file
        """
        # Use ParquetFile for streaming - avoids loading entire file into memory
        parquet_file = pq.ParquetFile(file_path)

        # Process in batches for memory efficiency
        current_batch: List[Dict[str, Any]] = []

        for batch in parquet_file.iter_batches(batch_size=self.batch_size):
            if not self._running:
                break

            # Convert batch to dictionary of lists
            batch_dict = batch.to_pydict()
            num_rows = len(batch_dict.get('trace_id', []))

            for i in range(num_rows):
                if not self._running:
                    break

                # Extract row data
                row = {col: batch_dict[col][i] for col in batch_dict}

                # Convert to span format
                span = self._convert_row_to_span(row)

                if span is None:
                    self.metrics.increment_skipped()
                    continue

                current_batch.append(span)

                # Send batch when full
                if len(current_batch) >= self.batch_size:
                    self._send_batch_with_backpressure(current_batch)
                    current_batch = []

        # Send remaining spans
        if current_batch and self._running:
            self._send_batch_with_backpressure(current_batch)

    def _convert_row_to_span(self, row: Dict[str, Any]) -> Optional[Dict[str, Any]]:
        """
        Convert a parquet row to a span dictionary.

        Validates required fields and handles data type conversions.

        Args:
            row: Dictionary containing parquet row data

        Returns:
            Span dictionary or None if validation fails
        """
        # Extract and validate required fields
        trace_id = str(row.get('trace_id', '') or '')
        span_id = str(row.get('span_id', '') or '')

        # Skip spans with missing required fields
        if not trace_id or not span_id:
            logger.debug(f"Skipping span with missing trace_id or span_id")
            return None

        # Handle parent_id (can be None for root spans)
        parent_id = row.get('parent_id')
        if parent_id is None:
            parent_id = ''
        else:
            parent_id = str(parent_id)

        # Extract timing fields with safe conversion
        start_time = self._to_int(row.get('timestamp_micros'))
        duration = self._to_int(row.get('duration_micros'))

        # Skip spans with invalid timing
        if start_time is None or start_time <= 0:
            logger.debug(f"Skipping span {span_id} with invalid start_time")
            return None

        if duration is None or duration < 0:
            logger.debug(f"Skipping span {span_id} with invalid duration")
            return None

        # Build tags from attributes
        tags = self._extract_tags(row)

        # Construct span object matching analyzer's expected format
        span = {
            'TraceID': trace_id,
            'SpanID': span_id,
            'ParentID': parent_id,
            'OperationName': str(row.get('name', '') or ''),
            'StartTime': start_time,
            'Duration': duration,
            'ServiceName': str(row.get('local_endpoint_service_name', '') or ''),
            'Tags': tags
        }

        return span

    def _to_int(self, value: Any) -> Optional[int]:
        """
        Safely convert a value to integer.

        Args:
            value: Value to convert

        Returns:
            Integer value or None if conversion fails
        """
        if value is None:
            return None
        try:
            return int(value)
        except (ValueError, TypeError):
            return None

    def _extract_tags(self, row: Dict[str, Any]) -> Dict[str, str]:
        """
        Extract tags from a parquet row.

        Looks for common tag columns and attribute maps.

        Args:
            row: Dictionary containing parquet row data

        Returns:
            Dictionary of string tags
        """
        tags: Dict[str, str] = {}

        # Common tag fields to extract
        tag_fields = [
            'http.method',
            'http.url',
            'http.status_code',
            'http.route',
            'db.system',
            'db.statement',
            'rpc.system',
            'rpc.method',
            'error',
            'otel.status_code',
            'otel.status_description',
        ]

        for field in tag_fields:
            # Try direct field access
            value = row.get(field)
            if value is not None:
                tags[field] = str(value)

            # Try with underscore instead of dot
            alt_field = field.replace('.', '_')
            value = row.get(alt_field)
            if value is not None and field not in tags:
                tags[field] = str(value)

        # Extract service identity fields for grouping
        identity_fields = [
            'service_identity',
            'scope1',
            'scope2',
            'scope3',
            'env',
            'feature_group',
            'feature_name',
            'sub_service',
        ]

        for field in identity_fields:
            value = row.get(field)
            if value is not None and value != '':
                tags[field] = str(value)

        # Handle attributes map if present
        attributes = row.get('attributes')
        if isinstance(attributes, dict):
            for key, value in attributes.items():
                if value is not None:
                    tags[str(key)] = str(value)

        return tags

    def _send_batch_with_backpressure(self, spans: List[Dict[str, Any]]) -> bool:
        """
        Send a batch of spans with backpressure handling.

        Monitors response times and adds delays if the analyzer is
        responding slowly to prevent overwhelming it.

        Args:
            spans: List of span dictionaries to send

        Returns:
            True if batch was sent successfully
        """
        # Apply backpressure delay if needed
        if self._current_delay > 0:
            logger.debug(f"Applying backpressure delay: {self._current_delay:.2f}s")
            time.sleep(self._current_delay)

        success = self._send_with_retry(spans)

        # Adjust backpressure based on response time
        if self.metrics.avg_response_time_ms > self._backpressure_threshold_ms:
            # Increase delay
            self._current_delay = min(self._current_delay + 0.5, 5.0)
            logger.warning(
                f"Response time {self.metrics.avg_response_time_ms:.0f}ms exceeds threshold, "
                f"increasing delay to {self._current_delay:.1f}s"
            )
        elif self._current_delay > 0 and self.metrics.avg_response_time_ms < self._backpressure_threshold_ms * 0.5:
            # Decrease delay
            self._current_delay = max(self._current_delay - 0.25, 0.0)
            if self._current_delay > 0:
                logger.info(f"Response time improved, decreasing delay to {self._current_delay:.1f}s")

        return success

    def _send_with_retry(self, spans: List[Dict[str, Any]]) -> bool:
        """
        Send a batch of spans with exponential backoff retry.

        Args:
            spans: List of span dictionaries to send

        Returns:
            True if batch was sent successfully after retries
        """
        for attempt in range(self.max_retries):
            try:
                if self._send_batch(spans):
                    return True
            except requests.exceptions.Timeout:
                logger.warning(f"Request timeout on attempt {attempt + 1}/{self.max_retries}")
            except requests.exceptions.ConnectionError as e:
                logger.warning(f"Connection error on attempt {attempt + 1}/{self.max_retries}: {e}")
            except Exception as e:
                logger.error(f"Unexpected error on attempt {attempt + 1}/{self.max_retries}: {e}")

            # Exponential backoff
            if attempt < self.max_retries - 1:
                delay = 2 ** attempt
                logger.info(f"Retrying in {delay}s...")
                time.sleep(delay)

        # All retries failed
        self.metrics.increment_batches_failed()
        self.metrics.increment_failed(len(spans))
        return False

    def _send_batch(self, spans: List[Dict[str, Any]]) -> bool:
        """
        Send a batch of spans to the trace analyzer.

        Args:
            spans: List of span dictionaries to send

        Returns:
            True if the batch was accepted by the analyzer
        """
        if not spans:
            return True

        start_time = time.time()

        try:
            response = self.session.post(
                f"{self.analyzer_url}/api/spans/batch",
                json=spans,
                headers={'Content-Type': 'application/json'},
                timeout=self.request_timeout
            )

            # Record response time
            elapsed_ms = (time.time() - start_time) * 1000
            self.metrics.record_response_time(elapsed_ms)

            if response.status_code == 202:
                # Parse response to get accepted/rejected counts
                result = response.json()
                accepted = result.get('accepted', len(spans))
                rejected = result.get('rejected', 0)

                self.metrics.increment_sent(accepted)
                self.metrics.increment_skipped(rejected)
                self.metrics.increment_batches_sent()

                logger.debug(
                    f"Batch sent: {accepted} accepted, {rejected} rejected, "
                    f"{elapsed_ms:.0f}ms"
                )
                return True
            else:
                logger.warning(
                    f"Batch rejected with status {response.status_code}: "
                    f"{response.text[:200]}"
                )
                return False

        except requests.exceptions.RequestException:
            raise
        except Exception as e:
            logger.error(f"Error sending batch: {e}")
            raise

    def _cleanup_old_entries(self) -> None:
        """
        Clean up old entries from processed sets to bound memory usage.

        Keeps only the most recent entries to prevent unbounded growth.
        """
        # Clean up processed files
        with self._files_lock:
            if len(self.processed_files) > self.DEFAULT_MAX_PROCESSED_FILES:
                # Keep only the most recent files (alphabetically sorted, which
                # works for date-based naming conventions)
                sorted_files = sorted(self.processed_files)
                keep_count = self.DEFAULT_MAX_PROCESSED_FILES // 2
                self.processed_files = set(sorted_files[-keep_count:])
                logger.info(
                    f"Cleaned up processed files set: kept {len(self.processed_files)} entries"
                )

        # Clean up processed timestamps
        with self._timestamps_lock:
            if len(self.processed_timestamps) > self.DEFAULT_MAX_PROCESSED_TIMESTAMPS:
                sorted_ts = sorted(self.processed_timestamps)
                keep_count = self.DEFAULT_MAX_PROCESSED_TIMESTAMPS // 2
                self.processed_timestamps = set(sorted_ts[-keep_count:])
                logger.info(
                    f"Cleaned up processed timestamps set: kept {len(self.processed_timestamps)} entries"
                )

    def get_status(self) -> Dict[str, Any]:
        """
        Get the current pipeline status.

        Returns:
            Dictionary containing pipeline status and metrics
        """
        return {
            'running': self._running,
            's3_bucket': self.s3_bucket,
            's3_prefix': self.s3_prefix,
            'analyzer_url': self.analyzer_url,
            'batch_size': self.batch_size,
            'backpressure_delay': self._current_delay,
            'processed_files_count': len(self.processed_files),
            'metrics': self.metrics.get_summary(),
        }


def parse_args() -> argparse.Namespace:
    """Parse command line arguments."""
    parser = argparse.ArgumentParser(
        description='Continuous pipeline for processing parquet span files from S3',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Environment variables:
  AWS_ACCESS_KEY_ID       AWS access key
  AWS_SECRET_ACCESS_KEY   AWS secret key
  AWS_DEFAULT_REGION      AWS region (default: us-east-1)
  ANALYZER_URL            URL of the trace analyzer service
  S3_BUCKET               S3 bucket containing parquet files
  S3_PREFIX               S3 prefix to filter files (optional)
  BATCH_SIZE              Number of spans per batch (default: 500)
  POLL_INTERVAL           Seconds between S3 polls (default: 30)
        """
    )

    parser.add_argument(
        '--s3-bucket',
        default=os.environ.get('S3_BUCKET'),
        help='S3 bucket containing parquet files'
    )
    parser.add_argument(
        '--s3-prefix',
        default=os.environ.get('S3_PREFIX', ''),
        help='S3 prefix to filter files'
    )
    parser.add_argument(
        '--analyzer-url',
        default=os.environ.get('ANALYZER_URL', 'http://localhost:8080'),
        help='URL of the trace analyzer service'
    )
    parser.add_argument(
        '--batch-size',
        type=int,
        default=int(os.environ.get('BATCH_SIZE', '500')),
        help='Number of spans per batch'
    )
    parser.add_argument(
        '--poll-interval',
        type=int,
        default=int(os.environ.get('POLL_INTERVAL', '30')),
        help='Seconds between S3 polls'
    )
    parser.add_argument(
        '--max-retries',
        type=int,
        default=3,
        help='Maximum retry attempts for failed requests'
    )
    parser.add_argument(
        '--request-timeout',
        type=int,
        default=30,
        help='Timeout in seconds for HTTP requests'
    )
    parser.add_argument(
        '--max-workers',
        type=int,
        default=4,
        help='Maximum number of worker threads'
    )
    parser.add_argument(
        '--aws-region',
        default=os.environ.get('AWS_DEFAULT_REGION', 'us-east-1'),
        help='AWS region for S3'
    )
    parser.add_argument(
        '--log-level',
        choices=['DEBUG', 'INFO', 'WARNING', 'ERROR'],
        default=os.environ.get('LOG_LEVEL', 'INFO'),
        help='Logging level'
    )

    return parser.parse_args()


def main() -> int:
    """Main entry point."""
    args = parse_args()

    # Set log level
    logging.getLogger().setLevel(getattr(logging, args.log_level))

    # Validate required arguments
    if not args.s3_bucket:
        logger.error("S3 bucket is required. Set --s3-bucket or S3_BUCKET environment variable.")
        return 1

    # Get AWS credentials from environment
    aws_access_key = os.environ.get('AWS_ACCESS_KEY_ID')
    aws_secret_key = os.environ.get('AWS_SECRET_ACCESS_KEY')

    # Create and start pipeline
    pipeline = ContinuousPipeline(
        s3_bucket=args.s3_bucket,
        analyzer_url=args.analyzer_url,
        s3_prefix=args.s3_prefix,
        batch_size=args.batch_size,
        poll_interval=args.poll_interval,
        max_retries=args.max_retries,
        request_timeout=args.request_timeout,
        max_workers=args.max_workers,
        aws_access_key=aws_access_key,
        aws_secret_key=aws_secret_key,
        aws_region=args.aws_region,
    )

    try:
        pipeline.start()
    except KeyboardInterrupt:
        logger.info("Interrupted by user")
    except Exception as e:
        logger.error(f"Pipeline failed: {e}", exc_info=True)
        return 1

    return 0


if __name__ == '__main__':
    sys.exit(main())
