package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog/log"
	"github.com/trace-analyzer/internal/models"
)

// Storage is the interface for trace storage backends
// Production-extensible: implement this interface for RocksDB, DuckDB, etc.
type Storage interface {
	// Trace operations
	StoreTrace(trace *models.Trace) error
	GetTrace(traceID string) (*models.Trace, error)
	GetRecentTraces(serviceID string, limit int) ([]*models.TraceSummary, error)
	GetTracesByFingerprint(serviceID, fingerprint string, limit int) ([]*models.TraceSummary, error)

	// Deviation operations
	StoreDeviation(deviation *models.Deviation, variant string) error
	// variant "" returns rows for every combination
	GetDeviations(serviceID, variant string, limit int, minScore float64) ([]*models.Deviation, error)

	// Anomaly operations
	StoreAnomaly(anomaly *models.Anomaly, variant string) error
	GetAnomalies(serviceID, variant string, limit int, minScore float64) ([]*models.Anomaly, error)

	// Rollup operations
	StoreRollup(rollup *models.Rollup) error

	// Maintenance
	Cleanup(olderThan time.Duration) error
	Stats() StorageStats
	StatsForVariant(variant string) StorageStats
	Close() error
}

// StorageStats holds storage metrics
type StorageStats struct {
	TraceCount     int64
	SpanCount      int64
	DeviationCount int64
	AnomalyCount   int64
	ErrorCount     int64
	RollupCount    int64
}

// ServiceIssueCountsProvider exposes per-service deviation/anomaly counts.
type ServiceIssueCountsProvider interface {
	GetServiceIssueCounts(variant string) (map[string]models.ServiceIssueCounts, error)
}

// InterestingTraceStore exposes anomaly/deviation trace retention.
type InterestingTraceStore interface {
	StoreInterestingTrace(trace *models.Trace, reason string, score float64, summary string, variant string) error
	GetInterestingTraces(reason string, limit int) ([]*models.InterestingTraceSummary, error)
	GetInterestingTrace(traceID string) (*models.InterestingTrace, error)
}

// SQLiteStorage implements Storage using SQLite (hackathon implementation)
type SQLiteStorage struct {
	db        *sql.DB
	retention time.Duration
	// Note: SQLite with WAL mode handles concurrency at database level,
	// but we still need mutex for read-modify-write operations
	mu                    sync.Mutex
	hasAnomalyFingerprint bool // Cached schema check - set after migration
}

// NewSQLiteStorage creates a new SQLite-based storage
func NewSQLiteStorage(dbPath string, retention time.Duration) (*SQLiteStorage, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	s := &SQLiteStorage{
		db:        db,
		retention: retention,
	}

	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	log.Info().Str("path", dbPath).Dur("retention", retention).Msg("SQLite storage initialized")
	return s, nil
}

func (s *SQLiteStorage) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS traces (
		trace_id TEXT PRIMARY KEY,
		service_id TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		spans_json TEXT NOT NULL,
		span_count INTEGER NOT NULL,
		duration_us INTEGER NOT NULL,
		has_error BOOLEAN NOT NULL,
		is_important BOOLEAN NOT NULL DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE INDEX IF NOT EXISTS idx_traces_service ON traces(service_id, created_at);
	CREATE INDEX IF NOT EXISTS idx_traces_fingerprint ON traces(fingerprint);
	CREATE INDEX IF NOT EXISTS idx_traces_created ON traces(created_at);
	
	CREATE TABLE IF NOT EXISTS deviations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		trace_id TEXT NOT NULL,
		service_id TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		canonical_fingerprint TEXT NOT NULL,
		score REAL NOT NULL,
		diff_summary TEXT,
		variant TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_deviations_service ON deviations(service_id, created_at);
	CREATE INDEX IF NOT EXISTS idx_deviations_score ON deviations(score);

	CREATE TABLE IF NOT EXISTS anomalies (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		trace_id TEXT NOT NULL,
		service_id TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		score REAL NOT NULL,
		slow_branches TEXT,
		variant TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_anomalies_service ON anomalies(service_id, created_at);
	CREATE INDEX IF NOT EXISTS idx_anomalies_score ON anomalies(score);
	-- Note: idx_anomalies_fingerprint and idx_traces_important created after migration

	` + fmt.Sprintf(interestingTracesDDL, "interesting_traces") + `;
	-- Note: interesting_traces indexes are created after its migrations
	
	CREATE TABLE IF NOT EXISTS rollups (
		window_start TIMESTAMP NOT NULL,
		window_end TIMESTAMP NOT NULL,
		service_id TEXT NOT NULL,
		service_identity TEXT,
		scope1 TEXT,
		scope2 TEXT,
		scope3 TEXT,
		env TEXT,
		operation TEXT,
		feature_group TEXT,
		feature_name TEXT,
		sub_service TEXT,
		fingerprint TEXT NOT NULL,
		trace_count INTEGER NOT NULL,
		span_count INTEGER NOT NULL,
		error_count INTEGER NOT NULL,
		duration_sum INTEGER NOT NULL,
		duration_min INTEGER NOT NULL,
		duration_max INTEGER NOT NULL,
		duration_p50 INTEGER,
		duration_p99 INTEGER,
		branch_stats TEXT,
		sample_trace_ids TEXT,
		PRIMARY KEY (window_start, service_id, fingerprint)
	);
	
	CREATE INDEX IF NOT EXISTS idx_rollups_service ON rollups(service_id, window_start);
	
	CREATE TABLE IF NOT EXISTS stats_metadata (
		key TEXT PRIMARY KEY,
		value INTEGER NOT NULL
	);
	
	CREATE TABLE IF NOT EXISTS pending_spans (
		trace_id TEXT NOT NULL,
		span_id TEXT NOT NULL,
		span_json TEXT NOT NULL,
		first_seen TIMESTAMP NOT NULL,
		last_seen TIMESTAMP NOT NULL,
		PRIMARY KEY (trace_id, span_id)
	);
	
	CREATE INDEX IF NOT EXISTS idx_pending_spans_trace ON pending_spans(trace_id, first_seen);
	CREATE INDEX IF NOT EXISTS idx_pending_spans_first_seen ON pending_spans(first_seen);

	CREATE TABLE IF NOT EXISTS baselines (
		service_id TEXT PRIMARY KEY,
		data_json TEXT NOT NULL,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS baseline_combinations (
		service_id TEXT NOT NULL,
		combination TEXT NOT NULL,
		data_json TEXT NOT NULL,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (service_id, combination)
	);

	CREATE TABLE IF NOT EXISTS hourly_baselines (
		hour TEXT NOT NULL,
		service_id TEXT NOT NULL,
		data_json TEXT NOT NULL,
		PRIMARY KEY (hour, service_id)
	);

	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	// Ensure rollup grouping columns exist for existing databases
	columns := map[string]string{
		"service_identity": "TEXT",
		"scope1":           "TEXT",
		"scope2":           "TEXT",
		"scope3":           "TEXT",
		"env":              "TEXT",
		"operation":        "TEXT",
		"feature_group":    "TEXT",
		"feature_name":     "TEXT",
		"sub_service":      "TEXT",
	}
	for col, colType := range columns {
		if err := s.addColumnIfMissing("rollups", col, colType); err != nil {
			return err
		}
	}

	// Ensure new columns exist for existing databases
	// Note: SQLite doesn't support DEFAULT values when adding columns to existing tables
	// So we add the column first, then update existing rows if needed
	if err := s.addColumnIfMissing("traces", "is_important", "BOOLEAN"); err != nil {
		log.Warn().Err(err).Msg("Failed to add is_important column to traces, continuing")
	} else {
		// Update existing rows to have is_important = 0
		s.migrate("UPDATE traces SET is_important = 0 WHERE is_important IS NULL")
	}

	if err := s.addColumnIfMissing("anomalies", "fingerprint", "TEXT"); err != nil {
		log.Warn().Err(err).Msg("Failed to add fingerprint column to anomalies, continuing")
		// Don't fail completely - old records will have empty fingerprint
	} else {
		// Create index after column is added
		s.migrate("CREATE INDEX IF NOT EXISTS idx_anomalies_fingerprint ON anomalies(fingerprint)")
		// Cache that fingerprint column exists to avoid PRAGMA checks on every query
		s.hasAnomalyFingerprint = true
	}

	// Create index for is_important after column is added
	s.migrate("CREATE INDEX IF NOT EXISTS idx_traces_important ON traces(is_important, created_at)")

	// Add composite indexes for better query performance
	s.migrate("CREATE INDEX IF NOT EXISTS idx_deviations_service_score ON deviations(service_id, score DESC, created_at DESC)")
	s.migrate("CREATE INDEX IF NOT EXISTS idx_anomalies_service_score ON anomalies(service_id, score DESC, created_at DESC)")
	s.migrate("CREATE INDEX IF NOT EXISTS idx_traces_service_fingerprint ON traces(service_id, fingerprint)")

	// Add variant column to existing tables for per-variant tracking
	if err := s.addColumnIfMissing("deviations", "variant", "TEXT NOT NULL DEFAULT ''"); err != nil {
		log.Warn().Err(err).Msg("Failed to add variant column to deviations, continuing")
	} else {
		s.migrate("CREATE INDEX IF NOT EXISTS idx_deviations_variant ON deviations(variant, created_at)")
	}

	if err := s.addColumnIfMissing("anomalies", "variant", "TEXT NOT NULL DEFAULT ''"); err != nil {
		log.Warn().Err(err).Msg("Failed to add variant column to anomalies, continuing")
	} else {
		s.migrate("CREATE INDEX IF NOT EXISTS idx_anomalies_variant ON anomalies(variant, created_at)")
	}

	if err := s.addColumnIfMissing("interesting_traces", "variant", "TEXT NOT NULL DEFAULT ''"); err != nil {
		log.Warn().Err(err).Msg("Failed to add variant column to interesting_traces, continuing")
	} else {
	}

	if err := s.migrateInterestingTracesKey(); err != nil {
		log.Warn().Err(err).Msg("Failed to migrate interesting_traces to (trace_id, reason) key, continuing")
	}
	s.migrate("CREATE INDEX IF NOT EXISTS idx_interesting_service ON interesting_traces(service_id, created_at)")
	s.migrate("CREATE INDEX IF NOT EXISTS idx_interesting_reason ON interesting_traces(reason, created_at)")
	s.migrate("CREATE INDEX IF NOT EXISTS idx_interesting_variant ON interesting_traces(variant, created_at)")

	return nil
}

// interestingTracesDDL keys rows by (trace_id, reason) so one trace can be
// kept as an error and as an anomaly or deviation at the same time.
const interestingTracesDDL = `CREATE TABLE IF NOT EXISTS %s (
		trace_id TEXT NOT NULL,
		service_id TEXT NOT NULL,
		fingerprint TEXT NOT NULL,
		reason TEXT NOT NULL,
		score REAL NOT NULL,
		summary TEXT,
		spans_json TEXT NOT NULL,
		span_count INTEGER NOT NULL,
		duration_us INTEGER NOT NULL,
		has_error BOOLEAN NOT NULL,
		variant TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP NOT NULL,
		PRIMARY KEY (trace_id, reason)
	)`

func (s *SQLiteStorage) migrate(stmt string) {
	if _, err := s.db.Exec(stmt); err != nil {
		log.Warn().Err(err).Str("statement", stmt).Msg("Schema migration statement failed, continuing")
	}
}

// migrateInterestingTracesKey rebuilds a legacy interesting_traces table keyed
// on trace_id alone. SQLite cannot change a primary key in place.
func (s *SQLiteStorage) migrateInterestingTracesKey() error {
	rows, err := s.db.Query("PRAGMA table_info(interesting_traces)")
	if err != nil {
		return err
	}
	pkColumns := 0
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if pk > 0 {
			pkColumns++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if pkColumns != 1 {
		return nil
	}

	const columns = "trace_id, service_id, fingerprint, reason, score, summary, spans_json, span_count, duration_us, has_error, variant, created_at"
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS interesting_traces_new",
		fmt.Sprintf(interestingTracesDDL, "interesting_traces_new"),
		"INSERT INTO interesting_traces_new (" + columns + ") SELECT " + columns + " FROM interesting_traces",
		"DROP TABLE interesting_traces",
		"ALTER TABLE interesting_traces_new RENAME TO interesting_traces",
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Info().Msg("Migrated interesting_traces to (trace_id, reason) key")
	return nil
}

func (s *SQLiteStorage) addColumnIfMissing(table, column, columnType string) error {
	// Check if table exists first
	var tableExists int
	err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&tableExists)
	if err != nil {
		return err
	}
	if tableExists == 0 {
		// Table doesn't exist yet, schema creation will handle it
		return nil
	}

	// Check if column exists
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil // Column already exists
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Column doesn't exist, add it
	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, columnType))
	return err
}

// StoreTrace stores a complete trace
func (s *SQLiteStorage) StoreTrace(trace *models.Trace) error {
	return s.StoreTraceWithImportance(trace, false)
}

// StoreInterestingTrace stores a trace for long-term reference (anomaly/deviation).
func (s *SQLiteStorage) StoreInterestingTrace(trace *models.Trace, reason string, score float64, summary string, variant string) error {
	if trace == nil {
		return fmt.Errorf("trace is nil")
	}
	spansJSON, err := json.Marshal(trace.Spans)
	if err != nil {
		return fmt.Errorf("marshal spans: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO interesting_traces
		(trace_id, service_id, fingerprint, reason, score, summary, spans_json, span_count, duration_us, has_error, variant, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, trace.TraceID, trace.ServiceID, trace.Fingerprint, reason, score, summary,
		string(spansJSON), len(trace.Spans), trace.TotalDurationUs, trace.HasError, variant, trace.StartTime)
	if err != nil {
		return err
	}

	// Keep this trace longer in hot tier as well.
	_ = s.MarkTraceImportant(trace.TraceID)
	return nil
}

// GetInterestingTraces returns recent interesting traces (anomaly/deviation).
func (s *SQLiteStorage) GetInterestingTraces(reason string, limit int) ([]*models.InterestingTraceSummary, error) {
	query := `
		SELECT trace_id, service_id, fingerprint, reason, score, summary, span_count, duration_us, has_error, created_at
		FROM interesting_traces
	`
	args := []interface{}{}
	if reason != "" {
		query += " WHERE reason = ?"
		args = append(args, reason)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*models.InterestingTraceSummary
	for rows.Next() {
		var t models.InterestingTraceSummary
		if err := rows.Scan(&t.TraceID, &t.ServiceID, &t.Fingerprint, &t.Reason, &t.Score,
			&t.Summary, &t.SpanCount, &t.DurationUs, &t.HasError, &t.Timestamp); err != nil {
			return nil, err
		}
		results = append(results, &t)
	}

	return results, rows.Err()
}

// GetInterestingTrace returns a stored interesting trace with spans.
func (s *SQLiteStorage) GetInterestingTrace(traceID string) (*models.InterestingTrace, error) {
	var t models.InterestingTrace
	var spansJSON string

	err := s.db.QueryRow(`
		SELECT trace_id, service_id, fingerprint, reason, score, summary, spans_json, created_at
		FROM interesting_traces WHERE trace_id = ?
		ORDER BY rowid DESC LIMIT 1
	`, traceID).Scan(&t.TraceID, &t.ServiceID, &t.Fingerprint, &t.Reason, &t.Score, &t.Summary, &spansJSON, &t.Timestamp)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(spansJSON), &t.Spans); err != nil {
		return nil, fmt.Errorf("unmarshal spans: %w", err)
	}

	return &t, nil
}

// StoreTraceWithImportance stores a trace and marks it as important
func (s *SQLiteStorage) StoreTraceWithImportance(trace *models.Trace, isImportant bool) error {
	spansJSON, err := json.Marshal(trace.Spans)
	if err != nil {
		return fmt.Errorf("marshal spans: %w", err)
	}

	// Use mutex to make check-then-insert atomic for counter updates
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if trace already exists before inserting
	var existingCount int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM traces WHERE trace_id = ?", trace.TraceID).Scan(&existingCount); err != nil {
		// Ignore error, assume new trace
		existingCount = 0
	}

	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO traces
		(trace_id, service_id, fingerprint, spans_json, span_count, duration_us, has_error, is_important)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, trace.TraceID, trace.ServiceID, trace.Fingerprint, string(spansJSON),
		len(trace.Spans), trace.TotalDurationUs, trace.HasError, isImportant)
	if err != nil {
		return err
	}

	// Increment cumulative trace counter only for new traces (never gets cleaned up)
	if existingCount == 0 {
		_, err = s.db.Exec(`
			INSERT INTO stats_metadata (key, value) VALUES ('total_traces_processed', 1)
			ON CONFLICT(key) DO UPDATE SET value = value + 1
		`)
		if err != nil {
			return err
		}
	}
	return nil
}

// MarkTraceImportant marks a trace as important (preserves it longer)
func (s *SQLiteStorage) MarkTraceImportant(traceID string) error {
	_, err := s.db.Exec(`UPDATE traces SET is_important = 1 WHERE trace_id = ?`, traceID)
	return err
}

// GetTrace retrieves a trace by ID
func (s *SQLiteStorage) GetTrace(traceID string) (*models.Trace, error) {
	var trace models.Trace
	var spansJSON string
	var spanCount int

	err := s.db.QueryRow(`
		SELECT trace_id, service_id, fingerprint, spans_json, span_count, duration_us, has_error
		FROM traces WHERE trace_id = ?
	`, traceID).Scan(&trace.TraceID, &trace.ServiceID, &trace.Fingerprint,
		&spansJSON, &spanCount, &trace.TotalDurationUs, &trace.HasError)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(spansJSON), &trace.Spans); err != nil {
		return nil, fmt.Errorf("unmarshal spans: %w", err)
	}

	// Find root span
	for _, span := range trace.Spans {
		if span.IsRoot() {
			trace.RootSpan = span
			break
		}
	}

	return &trace, nil
}

// GetRecentTraces returns recent traces for a service
func (s *SQLiteStorage) GetRecentTraces(serviceID string, limit int) ([]*models.TraceSummary, error) {
	query := `
		SELECT trace_id, service_id, fingerprint, span_count, duration_us, has_error, created_at
		FROM traces
	`
	args := []interface{}{}

	if serviceID != "" {
		query += " WHERE service_id = ?"
		args = append(args, serviceID)
	}

	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var traces []*models.TraceSummary
	for rows.Next() {
		var t models.TraceSummary
		if err := rows.Scan(&t.TraceID, &t.ServiceID, &t.Fingerprint,
			&t.SpanCount, &t.DurationUs, &t.HasError, &t.Timestamp); err != nil {
			return nil, err
		}
		traces = append(traces, &t)
	}

	return traces, rows.Err()
}

// GetServiceIssueCounts returns deviation/anomaly counts grouped by service_id.
// An empty variant counts every combination.
func (s *SQLiteStorage) GetServiceIssueCounts(variant string) (map[string]models.ServiceIssueCounts, error) {
	counts := make(map[string]models.ServiceIssueCounts)

	rows, err := s.db.Query(`SELECT service_id, COUNT(*) FROM deviations WHERE (? = '' OR variant = ?) GROUP BY service_id`, variant, variant)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var serviceID string
		var count int64
		if err := rows.Scan(&serviceID, &count); err != nil {
			rows.Close()
			return nil, err
		}
		entry := counts[serviceID]
		entry.DeviationCount = count
		counts[serviceID] = entry
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.Query(`SELECT service_id, COUNT(*) FROM anomalies WHERE (? = '' OR variant = ?) GROUP BY service_id`, variant, variant)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var serviceID string
		var count int64
		if err := rows.Scan(&serviceID, &count); err != nil {
			rows.Close()
			return nil, err
		}
		entry := counts[serviceID]
		entry.AnomalyCount = count
		counts[serviceID] = entry
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return counts, nil
}

// StoreDeviation stores a deviation record and marks the trace as important
func (s *SQLiteStorage) StoreDeviation(d *models.Deviation, variant string) error {
	_, err := s.db.Exec(`
		INSERT INTO deviations
		(trace_id, service_id, fingerprint, canonical_fingerprint, score, diff_summary, variant)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, d.TraceID, d.ServiceID, d.Fingerprint, d.CanonicalFingerprint, d.Score, d.DiffSummary, variant)
	if err != nil {
		return err
	}
	// Mark trace as important so it's preserved longer
	return s.MarkTraceImportant(d.TraceID)
}

// GetDeviations returns deviations for a service, limited to one combination unless variant is empty
func (s *SQLiteStorage) GetDeviations(serviceID, variant string, limit int, minScore float64) ([]*models.Deviation, error) {
	rows, err := s.db.Query(`
		SELECT trace_id, service_id, fingerprint, canonical_fingerprint, score, diff_summary, created_at
		FROM deviations
		WHERE service_id = ? AND score >= ? AND (? = '' OR variant = ?)
		ORDER BY created_at DESC
		LIMIT ?
	`, serviceID, minScore, variant, variant, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deviations []*models.Deviation
	for rows.Next() {
		var d models.Deviation
		if err := rows.Scan(&d.TraceID, &d.ServiceID, &d.Fingerprint,
			&d.CanonicalFingerprint, &d.Score, &d.DiffSummary, &d.Timestamp); err != nil {
			return nil, err
		}
		deviations = append(deviations, &d)
	}

	return deviations, rows.Err()
}

// StoreAnomaly stores an anomaly record and marks the trace as important
func (s *SQLiteStorage) StoreAnomaly(a *models.Anomaly, variant string) error {
	branchesJSON, _ := json.Marshal(a.SlowBranches)
	_, err := s.db.Exec(`
		INSERT INTO anomalies
		(trace_id, service_id, fingerprint, score, slow_branches, variant)
		VALUES (?, ?, ?, ?, ?, ?)
	`, a.TraceID, a.ServiceID, a.Fingerprint, a.Score, string(branchesJSON), variant)
	if err != nil {
		return err
	}
	// Mark trace as important so it's preserved longer
	return s.MarkTraceImportant(a.TraceID)
}

// GetAnomalies returns anomalies for a service, limited to one combination unless variant is empty
func (s *SQLiteStorage) GetAnomalies(serviceID, variant string, limit int, minScore float64) ([]*models.Anomaly, error) {
	// Use cached schema check instead of running PRAGMA each time
	hasFingerprint := s.hasAnomalyFingerprint

	var query string
	if hasFingerprint {
		query = `SELECT trace_id, service_id, fingerprint, score, slow_branches, created_at FROM anomalies WHERE service_id = ? AND score >= ? AND (? = '' OR variant = ?) ORDER BY created_at DESC LIMIT ?`
	} else {
		query = `SELECT trace_id, service_id, score, slow_branches, created_at FROM anomalies WHERE service_id = ? AND score >= ? AND (? = '' OR variant = ?) ORDER BY created_at DESC LIMIT ?`
	}

	rows, err := s.db.Query(query, serviceID, minScore, variant, variant, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var anomalies []*models.Anomaly
	for rows.Next() {
		var a models.Anomaly
		var branchesJSON string
		if hasFingerprint {
			if err := rows.Scan(&a.TraceID, &a.ServiceID, &a.Fingerprint, &a.Score, &branchesJSON, &a.Timestamp); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(&a.TraceID, &a.ServiceID, &a.Score, &branchesJSON, &a.Timestamp); err != nil {
				return nil, err
			}
			// Fingerprint will be empty for old records
			a.Fingerprint = ""
		}
		json.Unmarshal([]byte(branchesJSON), &a.SlowBranches)
		anomalies = append(anomalies, &a)
	}

	return anomalies, rows.Err()
}

// GetTracesByFingerprint returns traces with a specific fingerprint
func (s *SQLiteStorage) GetTracesByFingerprint(serviceID, fingerprint string, limit int) ([]*models.TraceSummary, error) {
	query := `
		SELECT trace_id, service_id, fingerprint, span_count, duration_us, has_error, created_at
		FROM traces
		WHERE service_id = ? AND fingerprint = ?
		ORDER BY created_at DESC
		LIMIT ?
	`
	rows, err := s.db.Query(query, serviceID, fingerprint, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var traces []*models.TraceSummary
	for rows.Next() {
		var t models.TraceSummary
		if err := rows.Scan(&t.TraceID, &t.ServiceID, &t.Fingerprint,
			&t.SpanCount, &t.DurationUs, &t.HasError, &t.Timestamp); err != nil {
			return nil, err
		}
		traces = append(traces, &t)
	}

	return traces, rows.Err()
}

// StoreRollup stores a rollup record, merging it into any row already stored
// for the same window, service and fingerprint.
func (s *SQLiteStorage) StoreRollup(r *models.Rollup) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existing models.Rollup
	var branchJSON, sampleJSON string
	err = tx.QueryRow(`
		SELECT trace_count, span_count, error_count, duration_sum, duration_min, duration_max,
		       duration_p50, duration_p99, branch_stats, sample_trace_ids
		FROM rollups WHERE window_start = ? AND service_id = ? AND fingerprint = ?
	`, r.WindowStart, r.ServiceID, r.Fingerprint).Scan(
		&existing.TraceCount, &existing.SpanCount, &existing.ErrorCount, &existing.DurationSum,
		&existing.DurationMin, &existing.DurationMax, &existing.DurationP50, &existing.DurationP99,
		&branchJSON, &sampleJSON)
	switch {
	case err == nil:
		json.Unmarshal([]byte(branchJSON), &existing.BranchStats)
		json.Unmarshal([]byte(sampleJSON), &existing.SampleTraceIDs)
		r = mergeRollups(&existing, r)
	case err != sql.ErrNoRows:
		return err
	}

	branchStatsJSON, _ := json.Marshal(r.BranchStats)
	sampleIDsJSON, _ := json.Marshal(r.SampleTraceIDs)
	grouping := r.ServiceGrouping

	_, err = tx.Exec(`
		INSERT OR REPLACE INTO rollups
		(window_start, window_end, service_id, service_identity, scope1, scope2, scope3, env, operation,
		 feature_group, feature_name, sub_service, fingerprint, trace_count, span_count, error_count,
		 duration_sum, duration_min, duration_max, duration_p50, duration_p99, branch_stats, sample_trace_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.WindowStart, r.WindowEnd, r.ServiceID,
		grouping.ServiceIdentity, grouping.Scope1, grouping.Scope2, grouping.Scope3, grouping.Env,
		grouping.Operation, grouping.FeatureGroup, grouping.FeatureName, grouping.SubService,
		r.Fingerprint, r.TraceCount, r.SpanCount, r.ErrorCount,
		r.DurationSum, r.DurationMin, r.DurationMax, r.DurationP50, r.DurationP99,
		string(branchStatsJSON), string(sampleIDsJSON))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// mergeRollups combines two partial rollups of the same window. Counts and sums
// add and min/max combine exactly. Percentiles cannot be merged from two
// summaries, so p50/p99 become the trace-count-weighted mean of both parts: an
// approximation that is exact only when the parts share a distribution.
func mergeRollups(old, cur *models.Rollup) *models.Rollup {
	merged := *cur
	total := old.TraceCount + cur.TraceCount
	merged.TraceCount = total
	merged.SpanCount = old.SpanCount + cur.SpanCount
	merged.ErrorCount = old.ErrorCount + cur.ErrorCount
	merged.DurationSum = old.DurationSum + cur.DurationSum
	merged.DurationMin = min(old.DurationMin, cur.DurationMin)
	merged.DurationMax = max(old.DurationMax, cur.DurationMax)
	if total > 0 {
		merged.DurationP50 = (old.DurationP50*old.TraceCount + cur.DurationP50*cur.TraceCount) / total
		merged.DurationP99 = (old.DurationP99*old.TraceCount + cur.DurationP99*cur.TraceCount) / total
	}

	merged.BranchStats = make(map[string]*models.BranchStats, len(old.BranchStats)+len(cur.BranchStats))
	for key, bs := range old.BranchStats {
		copied := *bs
		merged.BranchStats[key] = &copied
	}
	for key, bs := range cur.BranchStats {
		m, ok := merged.BranchStats[key]
		if !ok {
			copied := *bs
			merged.BranchStats[key] = &copied
			continue
		}
		m.Count += bs.Count
		m.SumDuration += bs.SumDuration
		m.MinDuration = min(m.MinDuration, bs.MinDuration)
		m.MaxDuration = max(m.MaxDuration, bs.MaxDuration)
		m.ErrorCount += bs.ErrorCount
	}

	merged.SampleTraceIDs = nil
	seen := make(map[string]bool)
	for _, id := range append(append([]string{}, old.SampleTraceIDs...), cur.SampleTraceIDs...) {
		if !seen[id] && len(merged.SampleTraceIDs) < maxRollupSampleIDs {
			seen[id] = true
			merged.SampleTraceIDs = append(merged.SampleTraceIDs, id)
		}
	}
	return &merged
}

// Cleanup removes data older than retention, but preserves important traces longer
func (s *SQLiteStorage) Cleanup(olderThan time.Duration) error {
	// created_at defaults to CURRENT_TIMESTAMP, which SQLite writes as UTC text.
	const sqliteTime = "2006-01-02 15:04:05"
	now := time.Now().UTC()
	cutoff := now.Add(-olderThan).Format(sqliteTime)
	// Important traces are kept 10x longer (e.g., 150 minutes instead of 15)
	importantCutoff := now.Add(-olderThan * 10).Format(sqliteTime)

	// Delete non-important traces older than cutoff, or important traces older than importantCutoff
	_, err := s.db.Exec(`
		DELETE FROM traces 
		WHERE (is_important = 0 AND created_at < ?) 
		   OR (is_important = 1 AND created_at < ?)
	`, cutoff, importantCutoff)
	if err != nil {
		return err
	}

	// Keep deviations/anomalies longer too - they reference important traces
	_, err = s.db.Exec("DELETE FROM deviations WHERE created_at < ?", importantCutoff)
	if err != nil {
		return err
	}

	_, err = s.db.Exec("DELETE FROM anomalies WHERE created_at < ?", importantCutoff)
	if err != nil {
		return err
	}

	// interesting_traces.created_at is the trace start time written with its zone
	// offset, so normalise it to UTC before comparing.
	_, err = s.db.Exec("DELETE FROM interesting_traces WHERE datetime(created_at) < ?", importantCutoff)
	return err
}

// GetRollupsBeforeCutoff returns all rollups with window_end before the cutoff time
func (s *SQLiteStorage) GetRollupsBeforeCutoff(cutoff time.Time) ([]*models.Rollup, error) {
	rows, err := s.db.Query(`
		SELECT window_start, window_end, service_id, service_identity, scope1, scope2, scope3, env, operation,
		       feature_group, feature_name, sub_service, fingerprint, trace_count, span_count, error_count,
		       duration_sum, duration_min, duration_max, duration_p50, duration_p99, branch_stats, sample_trace_ids
		FROM rollups
		WHERE window_end < ?
		ORDER BY window_start
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rollups []*models.Rollup
	for rows.Next() {
		var r models.Rollup
		var branchStatsJSON, sampleIDsJSON string
		if err := rows.Scan(&r.WindowStart, &r.WindowEnd, &r.ServiceID,
			&r.ServiceGrouping.ServiceIdentity, &r.ServiceGrouping.Scope1, &r.ServiceGrouping.Scope2,
			&r.ServiceGrouping.Scope3, &r.ServiceGrouping.Env, &r.ServiceGrouping.Operation,
			&r.ServiceGrouping.FeatureGroup, &r.ServiceGrouping.FeatureName, &r.ServiceGrouping.SubService,
			&r.Fingerprint,
			&r.TraceCount, &r.SpanCount, &r.ErrorCount, &r.DurationSum, &r.DurationMin,
			&r.DurationMax, &r.DurationP50, &r.DurationP99, &branchStatsJSON, &sampleIDsJSON); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(branchStatsJSON), &r.BranchStats)
		json.Unmarshal([]byte(sampleIDsJSON), &r.SampleTraceIDs)
		rollups = append(rollups, &r)
	}

	return rollups, rows.Err()
}

// DeleteRollupsBeforeCutoff removes rollups older than the cutoff
func (s *SQLiteStorage) DeleteRollupsBeforeCutoff(cutoff time.Time) (int64, error) {
	result, err := s.db.Exec("DELETE FROM rollups WHERE window_end < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Stats returns storage statistics
func (s *SQLiteStorage) Stats() StorageStats {
	var stats StorageStats

	// Use mutex for the initialization check to prevent race conditions
	s.mu.Lock()

	// Cumulative total traces processed (never drops, even after cleanup)
	var totalProcessed int64
	if err := s.db.QueryRow("SELECT COALESCE(value, 0) FROM stats_metadata WHERE key = 'total_traces_processed'").Scan(&totalProcessed); err != nil {
		totalProcessed = 0
	}

	// If counter doesn't exist yet, initialize it with current count
	if totalProcessed == 0 {
		var currentCount int64
		if err := s.db.QueryRow("SELECT COUNT(*) FROM traces").Scan(&currentCount); err == nil && currentCount > 0 {
			// Initialize counter with current count
			if _, err := s.db.Exec(`INSERT OR IGNORE INTO stats_metadata (key, value) VALUES ('total_traces_processed', ?)`, currentCount); err == nil {
				totalProcessed = currentCount
			}
		}
	}

	s.mu.Unlock()

	stats.TraceCount = totalProcessed

	// Single query with subqueries instead of 5 separate queries for better performance
	row := s.db.QueryRow(`
		SELECT
			(SELECT COALESCE(SUM(span_count), 0) FROM traces) as span_count,
			(SELECT COUNT(*) FROM deviations) as deviation_count,
			(SELECT COUNT(*) FROM anomalies) as anomaly_count,
			(SELECT COUNT(*) FROM interesting_traces WHERE reason = 'error') as error_count,
			(SELECT COUNT(*) FROM rollups) as rollup_count
	`)
	if err := row.Scan(&stats.SpanCount, &stats.DeviationCount, &stats.AnomalyCount,
		&stats.ErrorCount, &stats.RollupCount); err != nil {
		// Fallback to zeros on error
		stats.SpanCount = 0
		stats.DeviationCount = 0
		stats.AnomalyCount = 0
		stats.ErrorCount = 0
		stats.RollupCount = 0
	}

	return stats
}

// StatsForVariant returns storage statistics filtered by variant
func (s *SQLiteStorage) StatsForVariant(variant string) StorageStats {
	// For variant-specific stats, we only count deviations, anomalies, and interesting_traces
	// TraceCount and SpanCount are not filtered by variant as traces don't have variants
	total := s.Stats()
	stats := StorageStats{TraceCount: total.TraceCount, SpanCount: total.SpanCount, RollupCount: total.RollupCount}

	row := s.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM deviations WHERE variant = ?) as deviation_count,
			(SELECT COUNT(*) FROM anomalies WHERE variant = ?) as anomaly_count,
			(SELECT COUNT(*) FROM interesting_traces WHERE variant = ? AND reason = 'error') as error_count
	`, variant, variant, variant)
	if err := row.Scan(&stats.DeviationCount, &stats.AnomalyCount, &stats.ErrorCount); err != nil {
		// Fallback to zeros on error
		stats.DeviationCount = 0
		stats.AnomalyCount = 0
		stats.ErrorCount = 0
	}

	return stats
}

// GetDistinctInterestingTracesCount returns the number of distinct interesting trace IDs
// stored under variant. An empty variant counts every combination.
func (s *SQLiteStorage) GetDistinctInterestingTracesCount(variant string) (int64, error) {
	var count int64
	var err error
	if variant == "" {
		err = s.db.QueryRow("SELECT COUNT(DISTINCT trace_id) FROM interesting_traces").Scan(&count)
	} else {
		err = s.db.QueryRow("SELECT COUNT(DISTINCT trace_id) FROM interesting_traces WHERE variant = ?", variant).Scan(&count)
	}
	return count, err
}

// StorePendingSpan stores a pending span for trace assembly
func (s *SQLiteStorage) StorePendingSpan(traceID, spanID string, span *models.Span) error {
	spanJSON, err := json.Marshal(span)
	if err != nil {
		return fmt.Errorf("marshal span: %w", err)
	}

	now := time.Now()
	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO pending_spans (trace_id, span_id, span_json, first_seen, last_seen)
		VALUES (?, ?, ?, 
			COALESCE((SELECT first_seen FROM pending_spans WHERE trace_id = ? AND span_id = ?), ?),
			?)
	`, traceID, spanID, string(spanJSON), traceID, spanID, now, now)
	return err
}

// GetPendingSpans retrieves all pending spans for a trace
func (s *SQLiteStorage) GetPendingSpans(traceID string) ([]*models.Span, error) {
	rows, err := s.db.Query(`
		SELECT span_json FROM pending_spans
		WHERE trace_id = ?
		ORDER BY first_seen
	`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var spans []*models.Span
	for rows.Next() {
		var spanJSON string
		if err := rows.Scan(&spanJSON); err != nil {
			return nil, err
		}

		var span models.Span
		if err := json.Unmarshal([]byte(spanJSON), &span); err != nil {
			return nil, fmt.Errorf("unmarshal span: %w", err)
		}
		spans = append(spans, &span)
	}

	return spans, rows.Err()
}

// DeletePendingSpans removes all pending spans for a trace
func (s *SQLiteStorage) DeletePendingSpans(traceID string) error {
	_, err := s.db.Exec(`DELETE FROM pending_spans WHERE trace_id = ?`, traceID)
	return err
}

// GetExpiredPendingTraceIDs returns trace IDs that have expired
// If threshold > 0: returns trace-ids where first_seen + threshold < now (timer expired)
// If threshold == 0: returns all trace-ids (for shutdown)
func (s *SQLiteStorage) GetExpiredPendingTraceIDs(threshold time.Duration) ([]string, error) {
	var query string
	var args []interface{}

	if threshold == 0 {
		// Get all trace-ids (for shutdown)
		query = `SELECT DISTINCT trace_id FROM pending_spans ORDER BY first_seen`
		args = []interface{}{}
	} else {
		// Get trace-ids where timer expired: first_seen + threshold < now
		cutoff := time.Now().Add(-threshold)
		query = `SELECT DISTINCT trace_id FROM pending_spans WHERE first_seen < ? ORDER BY first_seen`
		args = []interface{}{cutoff}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var traceIDs []string
	for rows.Next() {
		var traceID string
		if err := rows.Scan(&traceID); err != nil {
			return nil, err
		}
		traceIDs = append(traceIDs, traceID)
	}

	return traceIDs, rows.Err()
}

// CleanupPendingSpans removes pending spans older than threshold
func (s *SQLiteStorage) CleanupPendingSpans(threshold time.Duration) error {
	cutoff := time.Now().Add(-threshold)
	_, err := s.db.Exec(`DELETE FROM pending_spans WHERE first_seen < ?`, cutoff)
	return err
}

// Close closes the database
func (s *SQLiteStorage) Close() error {
	return s.db.Close()
}

// RunCleanupLoop runs periodic cleanup
func (s *SQLiteStorage) RunCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Cleanup(s.retention); err != nil {
				log.Error().Err(err).Msg("Cleanup error")
			}
		}
	}
}

// BaselineData represents serializable baseline data for persistence
type BaselineData struct {
	ServiceID               string                         `json:"service_id"`
	ServiceGrouping         models.ServiceGrouping         `json:"service_grouping"`
	TopologyCounts          map[string]int64               `json:"topology_counts"`
	TopologyFirstSeen       map[string]time.Time           `json:"topology_first_seen"`
	KnownGoodPaths          map[string]bool                `json:"known_good_paths"`
	CanonicalFingerprint    string                         `json:"canonical_fingerprint"`
	TotalTraces             int64                          `json:"total_traces"`
	ErrorCount              int64                          `json:"error_count"`
	DurationSum             int64                          `json:"duration_sum"`
	DurationMin             int64                          `json:"duration_min"`
	DurationMax             int64                          `json:"duration_max"`
	DurationSamples         []int64                        `json:"duration_samples,omitempty"`
	PathBaselines           map[string]*PathBaselineData   `json:"path_baselines"`
	BranchBaselines         map[string]*BranchBaselineData `json:"branch_baselines"`
	TopologyFrequenciesEWMA map[string]float64             `json:"topology_frequencies_ewma"`
	LastUpdateTime          time.Time                      `json:"last_update_time"`
	LastSeen                time.Time                      `json:"last_seen"`
	Clock                   string                         `json:"clock,omitempty"`
}

// PathBaselineData represents serializable path baseline
type PathBaselineData struct {
	Count          int64     `json:"count"`
	SumDuration    int64     `json:"sum_duration"`
	MinDuration    int64     `json:"min_duration"`
	MaxDuration    int64     `json:"max_duration"`
	Samples        []int64   `json:"samples"`
	LastUpdateTime time.Time `json:"last_update_time,omitempty"`
}

// BranchBaselineData represents serializable branch baseline
type BranchBaselineData struct {
	Count       int64   `json:"count"`
	SumDuration int64   `json:"sum_duration"`
	MinDuration int64   `json:"min_duration"`
	MaxDuration int64   `json:"max_duration"`
	ErrorCount  int64   `json:"error_count"`
	Samples     []int64 `json:"samples"`
}

// SaveCombinationBaselines writes the rows fill passes to put, keyed by
// (service, combination), in one transaction. Any error rolls the whole save
// back, so an earlier save stays intact.
func (s *SQLiteStorage) SaveCombinationBaselines(fill func(put func(serviceID, combination string, data *BaselineData) error) error) (rows, bytes int, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("begin baseline save: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	stmt, err := tx.Prepare(`
		INSERT INTO baseline_combinations (service_id, combination, data_json, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(service_id, combination) DO UPDATE SET
			data_json = excluded.data_json,
			updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare baseline save: %w", err)
	}
	defer stmt.Close()

	err = fill(func(serviceID, combination string, data *BaselineData) error {
		raw, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("marshal baseline %s/%s: %w", serviceID, combination, err)
		}
		if _, err := stmt.Exec(serviceID, combination, raw); err != nil {
			return fmt.Errorf("store baseline %s/%s: %w", serviceID, combination, err)
		}
		rows++
		bytes += len(raw)
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit baseline save: %w", err)
	}
	return rows, bytes, nil
}

// CombinationBaselineKeys lists the stored (service, combination) pairs.
func (s *SQLiteStorage) CombinationBaselineKeys() (map[string]map[string]bool, error) {
	rows, err := s.db.Query(`SELECT service_id, combination FROM baseline_combinations`)
	if err != nil {
		return nil, fmt.Errorf("query baseline keys: %w", err)
	}
	defer rows.Close()
	keys := make(map[string]map[string]bool)
	for rows.Next() {
		var service, combination string
		if err := rows.Scan(&service, &combination); err != nil {
			return nil, fmt.Errorf("scan baseline key: %w", err)
		}
		if keys[service] == nil {
			keys[service] = make(map[string]bool)
		}
		keys[service][combination] = true
	}
	return keys, rows.Err()
}

// ForEachCombinationBaseline streams every stored combination row to fn, one
// at a time. Rows that fail to decode are skipped, logged and counted.
func (s *SQLiteStorage) ForEachCombinationBaseline(fn func(serviceID, combination string, data *BaselineData)) (corrupt int, err error) {
	rows, err := s.db.Query(`SELECT service_id, combination, data_json FROM baseline_combinations ORDER BY service_id, combination`)
	if err != nil {
		return 0, fmt.Errorf("query baseline combinations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var service, combination string
		var raw []byte
		if err := rows.Scan(&service, &combination, &raw); err != nil {
			return corrupt, fmt.Errorf("scan baseline combination: %w", err)
		}
		var data BaselineData
		if err := json.Unmarshal(raw, &data); err != nil {
			corrupt++
			log.Warn().Err(err).Str("service", service).Str("combination", combination).Msg("Skipping corrupt baseline row")
			continue
		}
		fn(service, combination, &data)
	}
	return corrupt, rows.Err()
}

// HourlyBaselineRow is one persisted same_time_yesterday (hour, service)
// bucket. DataJSON is opaque to storage.
type HourlyBaselineRow struct {
	Hour      string
	ServiceID string
	DataJSON  []byte
}

// SaveHourlyBaselines upserts rows and, when retained is non-empty, deletes
// rows whose hour is not in it, in one transaction.
func (s *SQLiteStorage) SaveHourlyBaselines(rows []HourlyBaselineRow, retained []string) (written int, deleted int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("begin hourly save: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	if len(rows) > 0 {
		stmt, err := tx.Prepare(`
			INSERT INTO hourly_baselines (hour, service_id, data_json) VALUES (?, ?, ?)
			ON CONFLICT(hour, service_id) DO UPDATE SET data_json = excluded.data_json
		`)
		if err != nil {
			return 0, 0, fmt.Errorf("prepare hourly save: %w", err)
		}
		defer stmt.Close()
		for _, r := range rows {
			if _, err := stmt.Exec(r.Hour, r.ServiceID, r.DataJSON); err != nil {
				return 0, 0, fmt.Errorf("store hourly %s/%s: %w", r.Hour, r.ServiceID, err)
			}
		}
	}
	if len(retained) > 0 {
		keys, err := json.Marshal(retained)
		if err != nil {
			return 0, 0, fmt.Errorf("marshal retained hours: %w", err)
		}
		res, err := tx.Exec(`DELETE FROM hourly_baselines WHERE hour NOT IN (SELECT value FROM json_each(?))`, string(keys))
		if err != nil {
			return 0, 0, fmt.Errorf("delete stale hourly rows: %w", err)
		}
		deleted, _ = res.RowsAffected()
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit hourly save: %w", err)
	}
	return len(rows), deleted, nil
}

// LoadHourlyBaselines returns every persisted hourly bucket row.
func (s *SQLiteStorage) LoadHourlyBaselines() ([]HourlyBaselineRow, error) {
	rows, err := s.db.Query(`SELECT hour, service_id, data_json FROM hourly_baselines ORDER BY hour, service_id`)
	if err != nil {
		return nil, fmt.Errorf("query hourly baselines: %w", err)
	}
	defer rows.Close()
	var out []HourlyBaselineRow
	for rows.Next() {
		var r HourlyBaselineRow
		if err := rows.Scan(&r.Hour, &r.ServiceID, &r.DataJSON); err != nil {
			return nil, fmt.Errorf("scan hourly baseline: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const activeCombinationKey = "active_combination"

// SaveActiveCombination records the combination chosen through the API.
func (s *SQLiteStorage) SaveActiveCombination(combination string) error {
	_, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, activeCombinationKey, combination)
	if err != nil {
		return fmt.Errorf("save active combination: %w", err)
	}
	return nil
}

// LoadActiveCombination returns the saved combination, or "" when none is.
func (s *SQLiteStorage) LoadActiveCombination() (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, activeCombinationKey).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load active combination: %w", err)
	}
	return v, nil
}

// StoreBaseline writes the legacy single-row-per-service table. The analyzer
// only reads that table now (as a fallback); this remains for tests and tools.
func (s *SQLiteStorage) StoreBaseline(data *BaselineData) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal baseline: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO baselines (service_id, data_json, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(service_id) DO UPDATE SET
			data_json = excluded.data_json,
			updated_at = CURRENT_TIMESTAMP
	`, data.ServiceID, string(jsonData))
	if err != nil {
		return fmt.Errorf("store baseline: %w", err)
	}

	return nil
}

// LoadBaselines loads all persisted baselines from the database
func (s *SQLiteStorage) LoadBaselines() ([]*BaselineData, error) {
	rows, err := s.db.Query(`SELECT service_id, data_json FROM baselines`)
	if err != nil {
		return nil, fmt.Errorf("query baselines: %w", err)
	}
	defer rows.Close()

	var baselines []*BaselineData
	for rows.Next() {
		var serviceID string
		var jsonData string
		if err := rows.Scan(&serviceID, &jsonData); err != nil {
			log.Warn().Err(err).Str("service", serviceID).Msg("Failed to scan baseline row")
			continue
		}

		var data BaselineData
		if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
			log.Warn().Err(err).Str("service", serviceID).Msg("Failed to unmarshal baseline")
			continue
		}

		baselines = append(baselines, &data)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate baselines: %w", err)
	}

	log.Info().Int("count", len(baselines)).Msg("Loaded baselines from database")
	return baselines, nil
}

// DeleteBaseline removes a service baseline from the database
func (s *SQLiteStorage) DeleteBaseline(serviceID string) error {
	_, err := s.db.Exec(`DELETE FROM baselines WHERE service_id = ?`, serviceID)
	return err
}
