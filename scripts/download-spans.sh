#!/usr/bin/env bash
#
# download-spans.sh - Download span parquet files from S3 buckets
#
# This script downloads parquet files from multiple S3 buckets in parallel.
# It supports retry logic, progress indicators, and various output options.
#
# Usage: ./download-spans.sh [OPTIONS] <timestamp>
#
# Options:
#   -d, --dir DIR       Download directory (default: ./spans)
#   -p, --parallel N    Number of parallel downloads (default: 16)
#   -r, --retries N     Max retry attempts per bucket (default: 3)
#   -n, --dry-run       Preview what would be downloaded without downloading
#   -q, --quiet         Suppress non-essential output
#   -v, --verbose       Show detailed output including s5cmd progress
#   -h, --help          Show this help message
#
# Environment variables:
#   S3_BUCKET_NAME      S3 bucket name (required)
#   S3_PREFIX           S3 path prefix (default: spans)
#   AWS_PROFILE         AWS profile to use for s5cmd
#
# Examples:
#   S3_BUCKET_NAME=my-span-bucket ./download-spans.sh 2024-01-15T10:30:00
#   S3_BUCKET_NAME=my-span-bucket ./download-spans.sh --dry-run 2024-01-15T10:30:00
#

set -euo pipefail

# ==============================================================================
# Configuration
# ==============================================================================

# S3 bucket configuration from environment variables
readonly S3_BUCKET="${S3_BUCKET_NAME:-}"
readonly S3_PREFIX="${S3_PREFIX:-spans}"

# Default values
readonly DEFAULT_DOWNLOAD_DIR="./spans"
readonly DEFAULT_PARALLEL_JOBS=16
readonly DEFAULT_MAX_RETRIES=3
readonly TOTAL_BUCKETS=128

# Script state
DOWNLOAD_DIR="$DEFAULT_DOWNLOAD_DIR"
PARALLEL_JOBS="$DEFAULT_PARALLEL_JOBS"
MAX_RETRIES="$DEFAULT_MAX_RETRIES"
DRY_RUN=false
QUIET=false
VERBOSE=false
TIMESTAMP=""

# Counters (will be managed via temp files for parallel execution)
TEMP_DIR=""

# ==============================================================================
# Utility Functions
# ==============================================================================

# Print error message to stderr
error() {
    echo "ERROR: $*" >&2
}

# Print warning message to stderr
warn() {
    echo "WARNING: $*" >&2
}

# Print info message (respects quiet mode)
info() {
    if [[ "$QUIET" != true ]]; then
        echo "$*"
    fi
}

# Print debug message (only in verbose mode)
debug() {
    if [[ "$VERBOSE" == true ]]; then
        echo "DEBUG: $*"
    fi
}

# Validate S3 path to prevent command injection
validate_s3_path() {
    local path="$1"
    local name="$2"
    if [[ "$path" =~ [[:cntrl:]] ]] || [[ "$path" =~ [\`\$\(\)\;] ]]; then
        error "Invalid characters in $name: $path"
        exit 1
    fi
}

# Print usage information
usage() {
    cat << 'EOF'
Usage: download-spans.sh [OPTIONS] <timestamp>

Download span parquet files from S3 buckets.

Arguments:
  timestamp           Timestamp in ISO 8601 format (e.g., 2024-01-15T10:30:00)

Options:
  -d, --dir DIR       Download directory (default: ./spans)
  -p, --parallel N    Number of parallel downloads (default: 16)
  -r, --retries N     Max retry attempts per bucket (default: 3)
  -n, --dry-run       Preview what would be downloaded without downloading
  -q, --quiet         Suppress non-essential output
  -v, --verbose       Show detailed output including s5cmd progress
  -h, --help          Show this help message

Environment variables:
  S3_BUCKET_NAME      S3 bucket name (required)
  S3_PREFIX           S3 path prefix (default: spans)
  AWS_PROFILE         AWS profile to use for s5cmd

Examples:
  S3_BUCKET_NAME=my-span-bucket ./download-spans.sh 2024-01-15T10:30:00
  S3_BUCKET_NAME=my-span-bucket ./download-spans.sh --dry-run 2024-01-15T10:30:00
  S3_BUCKET_NAME=my-span-bucket ./download-spans.sh -d /data/spans -p 32 2024-01-15T10:30:00
EOF
}

# Convert timestamp to epoch seconds (supports both BSD and GNU date)
time_to_epoch() {
    local timestamp="$1"

    # Check if we have GNU date or BSD date
    if date --version >/dev/null 2>&1; then
        # GNU date (Linux)
        date -d "$timestamp" "+%s" 2>/dev/null
    else
        # BSD date (macOS)
        # Try ISO 8601 format first
        date -j -f "%Y-%m-%dT%H:%M:%S" "$timestamp" "+%s" 2>/dev/null || \
        date -j -f "%Y-%m-%d %H:%M:%S" "$timestamp" "+%s" 2>/dev/null || \
        date -j -f "%Y-%m-%dT%H:%M:%SZ" "$timestamp" "+%s" 2>/dev/null
    fi
}

# Convert epoch to formatted date string (supports both BSD and GNU date)
epoch_to_date() {
    local epoch="$1"
    local format="$2"

    if date --version >/dev/null 2>&1; then
        # GNU date
        date -d "@$epoch" "+$format"
    else
        # BSD date
        date -r "$epoch" "+$format"
    fi
}

# Check if required tools are available
check_dependencies() {
    local missing=()

    if ! command -v s5cmd >/dev/null 2>&1; then
        missing+=("s5cmd")
    fi

    # Check for parallel execution capability
    if ! command -v xargs >/dev/null 2>&1; then
        missing+=("xargs")
    fi

    if [[ ${#missing[@]} -gt 0 ]]; then
        error "Missing required tools: ${missing[*]}"
        echo "Please install the missing tools and try again." >&2
        exit 1
    fi
}

# Validate timestamp format
validate_timestamp() {
    local timestamp="$1"

    if [[ -z "$timestamp" ]]; then
        error "Timestamp is required"
        return 1
    fi

    if ! time_to_epoch "$timestamp" >/dev/null 2>&1; then
        error "Invalid timestamp format: $timestamp"
        echo "Expected format: YYYY-MM-DDTHH:MM:SS (e.g., 2024-01-15T10:30:00)" >&2
        return 1
    fi

    return 0
}

# ==============================================================================
# Download Functions
# ==============================================================================

# Build S3 path for a given bucket number and timestamp
build_s3_path() {
    local timestamp="$1"
    local bucket_num="$2"

    local epoch
    epoch=$(time_to_epoch "$timestamp")

    local year month day hour
    year=$(epoch_to_date "$epoch" "%Y")
    month=$(epoch_to_date "$epoch" "%m")
    day=$(epoch_to_date "$epoch" "%d")
    hour=$(epoch_to_date "$epoch" "%H")

    # Format bucket number with leading zeros (3 digits)
    local bucket_padded
    bucket_padded=$(printf "%03d" "$bucket_num")

    echo "s3://${S3_BUCKET}/${S3_PREFIX}/bucket=${bucket_padded}/year=${year}/month=${month}/day=${day}/hour=${hour}/"
}

# Build local destination path for a given bucket
build_local_path() {
    local timestamp="$1"
    local bucket_num="$2"

    local epoch
    epoch=$(time_to_epoch "$timestamp")

    local year month day hour
    year=$(epoch_to_date "$epoch" "%Y")
    month=$(epoch_to_date "$epoch" "%m")
    day=$(epoch_to_date "$epoch" "%d")
    hour=$(epoch_to_date "$epoch" "%H")

    local bucket_padded
    bucket_padded=$(printf "%03d" "$bucket_num")

    echo "${DOWNLOAD_DIR}/bucket=${bucket_padded}/year=${year}/month=${month}/day=${day}/hour=${hour}/"
}

# Download a single bucket (called by parallel execution)
# Arguments: timestamp bucket_num temp_dir dry_run quiet verbose max_retries download_dir s3_bucket s3_prefix
download_bucket() {
    local timestamp="$1"
    local bucket_num="$2"
    local temp_dir="$3"
    local dry_run="$4"
    local quiet="$5"
    local verbose="$6"
    local max_retries="$7"
    local download_dir="$8"
    local s3_bucket="$9"
    local s3_prefix="${10}"

    local s3_path local_path

    # Rebuild paths using local variables
    local epoch year month day hour bucket_padded

    # Convert timestamp to epoch (inline for subprocess)
    if date --version >/dev/null 2>&1; then
        epoch=$(date -d "$timestamp" "+%s" 2>/dev/null)
    else
        epoch=$(date -j -f "%Y-%m-%dT%H:%M:%S" "$timestamp" "+%s" 2>/dev/null || \
                date -j -f "%Y-%m-%d %H:%M:%S" "$timestamp" "+%s" 2>/dev/null || \
                date -j -f "%Y-%m-%dT%H:%M:%SZ" "$timestamp" "+%s" 2>/dev/null)
    fi

    # Format date components
    if date --version >/dev/null 2>&1; then
        year=$(date -d "@$epoch" "+%Y")
        month=$(date -d "@$epoch" "+%m")
        day=$(date -d "@$epoch" "+%d")
        hour=$(date -d "@$epoch" "+%H")
    else
        year=$(date -r "$epoch" "+%Y")
        month=$(date -r "$epoch" "+%m")
        day=$(date -r "$epoch" "+%d")
        hour=$(date -r "$epoch" "+%H")
    fi

    bucket_padded=$(printf "%03d" "$bucket_num")

    s3_path="s3://${s3_bucket}/${s3_prefix}/bucket=${bucket_padded}/year=${year}/month=${month}/day=${day}/hour=${hour}/"
    local_path="${download_dir}/bucket=${bucket_padded}/year=${year}/month=${month}/day=${day}/hour=${hour}/"

    if [[ "$dry_run" == true ]]; then
        echo "[DRY-RUN] Would download: $s3_path -> $local_path"
        echo "$bucket_num" >> "${temp_dir}/success.txt"
        return 0
    fi

    # Create local directory
    mkdir -p "$local_path"

    local attempt=1
    local success=false

    while [[ $attempt -le $max_retries ]]; do
        if [[ "$verbose" == true ]]; then
            echo "Downloading bucket $bucket_num (attempt $attempt/$max_retries)..."
        fi

        # Build s5cmd command
        local s5cmd_args=()
        if [[ "$verbose" != true ]]; then
            s5cmd_args+=("--no-sign-request" "--log" "error")
        fi

        # Execute s5cmd cp
        local output
        if output=$(s5cmd "${s5cmd_args[@]}" cp "${s3_path}*.parquet" "$local_path" 2>&1); then
            # Check if any files were actually downloaded
            local file_count
            file_count=$(find "$local_path" -name "*.parquet" -type f 2>/dev/null | wc -l)

            if [[ $file_count -gt 0 ]]; then
                if [[ "$quiet" != true ]]; then
                    echo "Bucket $bucket_num: Downloaded $file_count file(s)"
                fi
                echo "$bucket_num:$file_count" >> "${temp_dir}/success.txt"
                success=true
                break
            else
                # No files found, but not necessarily an error
                if [[ "$verbose" == true ]]; then
                    echo "Bucket $bucket_num: No files found"
                fi
                echo "$bucket_num:0" >> "${temp_dir}/success.txt"
                success=true
                break
            fi
        else
            if [[ "$verbose" == true ]]; then
                echo "Bucket $bucket_num attempt $attempt failed: $output" >&2
            fi

            if [[ $attempt -lt $max_retries ]]; then
                local sleep_time=$((attempt * 2))
                if [[ "$verbose" == true ]]; then
                    echo "Retrying bucket $bucket_num in ${sleep_time}s..."
                fi
                sleep "$sleep_time"
            fi
        fi

        ((attempt++))
    done

    if [[ "$success" != true ]]; then
        echo "ERROR: Failed to download bucket $bucket_num after $max_retries attempts" >&2
        echo "$bucket_num" >> "${temp_dir}/failed.txt"
        return 1
    fi

    return 0
}

# Export function for use with xargs
export -f download_bucket

# ==============================================================================
# Main Execution
# ==============================================================================

main() {
    local start_time
    start_time=$(date +%s)

    # Parse command line arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            -d|--dir)
                DOWNLOAD_DIR="$2"
                shift 2
                ;;
            -p|--parallel)
                PARALLEL_JOBS="$2"
                shift 2
                ;;
            -r|--retries)
                MAX_RETRIES="$2"
                shift 2
                ;;
            -n|--dry-run)
                DRY_RUN=true
                shift
                ;;
            -q|--quiet)
                QUIET=true
                shift
                ;;
            -v|--verbose)
                VERBOSE=true
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            -*)
                error "Unknown option: $1"
                usage
                exit 1
                ;;
            *)
                if [[ -z "$TIMESTAMP" ]]; then
                    TIMESTAMP="$1"
                else
                    error "Unexpected argument: $1"
                    usage
                    exit 1
                fi
                shift
                ;;
        esac
    done

    # Validate inputs
    if [[ -z "$TIMESTAMP" ]]; then
        error "Timestamp is required"
        usage
        exit 1
    fi

    if ! validate_timestamp "$TIMESTAMP"; then
        exit 1
    fi

    if [[ -z "$S3_BUCKET" ]]; then
        error "S3_BUCKET_NAME is required"
        usage
        exit 1
    fi

    # Validate S3 path inputs to prevent command injection
    validate_s3_path "$S3_BUCKET" "S3_BUCKET_NAME"
    validate_s3_path "$S3_PREFIX" "S3_PREFIX"

    # Check dependencies (skip for dry-run if s5cmd not needed)
    if [[ "$DRY_RUN" != true ]]; then
        check_dependencies
    fi

    # Create temp directory for tracking results
    TEMP_DIR=$(mktemp -d)
    trap 'rm -rf "$TEMP_DIR"' EXIT

    touch "${TEMP_DIR}/success.txt"
    touch "${TEMP_DIR}/failed.txt"

    # Print configuration
    info "========================================"
    info "Span Download Configuration"
    info "========================================"
    info "Timestamp:     $TIMESTAMP"
    info "S3 Bucket:     $S3_BUCKET"
    info "S3 Prefix:     $S3_PREFIX"
    info "Download Dir:  $DOWNLOAD_DIR"
    info "Parallel Jobs: $PARALLEL_JOBS"
    info "Max Retries:   $MAX_RETRIES"
    info "Dry Run:       $DRY_RUN"
    info "========================================"
    info ""

    # Create download directory
    if [[ "$DRY_RUN" != true ]]; then
        mkdir -p "$DOWNLOAD_DIR"
    fi

    info "Starting download of $TOTAL_BUCKETS buckets..."
    info ""

    # Run parallel downloads
    # We use xargs with -P for parallel execution
    # Each bucket download is independent
    seq 0 $((TOTAL_BUCKETS - 1)) | xargs -P "$PARALLEL_JOBS" -I {} bash -c \
        'download_bucket "$1" "$2" "$3" "$4" "$5" "$6" "$7" "$8" "$9" "${10}"' \
        _ "$TIMESTAMP" {} "$TEMP_DIR" "$DRY_RUN" "$QUIET" "$VERBOSE" "$MAX_RETRIES" "$DOWNLOAD_DIR" "$S3_BUCKET" "$S3_PREFIX"

    # Wait for all background jobs and collect results
    local end_time elapsed
    end_time=$(date +%s)
    elapsed=$((end_time - start_time))

    # Count results
    local successful_count=0
    local failed_count=0
    local total_files=0

    if [[ -f "${TEMP_DIR}/success.txt" ]]; then
        successful_count=$(wc -l < "${TEMP_DIR}/success.txt" | tr -d ' ')

        # Count total files downloaded
        while IFS=: read -r bucket files; do
            if [[ -n "$files" ]]; then
                total_files=$((total_files + files))
            fi
        done < "${TEMP_DIR}/success.txt"
    fi

    if [[ -f "${TEMP_DIR}/failed.txt" ]]; then
        failed_count=$(wc -l < "${TEMP_DIR}/failed.txt" | tr -d ' ')
    fi

    # Clean up empty directories
    if [[ "$DRY_RUN" != true ]] && [[ -d "$DOWNLOAD_DIR" ]]; then
        find "$DOWNLOAD_DIR" -type d -empty -delete 2>/dev/null || true
    fi

    # Print summary
    echo ""
    echo "========================================"
    echo "Download Summary"
    echo "========================================"
    echo "  Total buckets:    $TOTAL_BUCKETS"
    echo "  Successful:       $successful_count"
    echo "  Failed:           $failed_count"
    echo "  Total files:      $total_files"
    echo "  Duration:         ${elapsed}s"
    echo "  Download dir:     $DOWNLOAD_DIR"
    echo "========================================"

    # List failed buckets if any
    if [[ $failed_count -gt 0 ]]; then
        echo ""
        echo "Failed buckets:"
        sort -n "${TEMP_DIR}/failed.txt" | while read -r bucket; do
            echo "  - Bucket $bucket"
        done
    fi

    # Return appropriate exit code
    if [[ $failed_count -gt 0 ]]; then
        exit 1
    fi

    exit 0
}

# Run main function
main "$@"
