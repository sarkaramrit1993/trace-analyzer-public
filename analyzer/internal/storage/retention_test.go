package storage

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/models"
)

func openTestStore(t *testing.T, path string) *SQLiteStorage {
	t.Helper()
	s, err := NewSQLiteStorage(path, 15*time.Minute)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleTrace(id string, start time.Time) *models.Trace {
	return &models.Trace{
		TraceID: id, ServiceID: "svc", Fingerprint: "fp", StartTime: start, TotalDurationUs: 1000, HasError: true,
		Spans: []*models.Span{{TraceID: id, SpanID: id + "-s", ServiceName: "svc", OperationName: "op"}},
	}
}

func interestingReasons(t *testing.T, s *SQLiteStorage, traceID string) map[string]bool {
	t.Helper()
	rows, err := s.db.Query("SELECT reason FROM interesting_traces WHERE trace_id = ?", traceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var r string
		rows.Scan(&r)
		out[r] = true
	}
	return out
}

func TestInterestingTraces_ErrorKeptWhenAlsoAnomaly(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "t.db"))
	tr := sampleTrace("t1", time.Now())
	s.StoreTrace(tr)
	if err := s.StoreInterestingTrace(tr, "error", 0, "err", "cumulative:50"); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreInterestingTrace(tr, "anomaly", 7, "slow", "cumulative:50"); err != nil {
		t.Fatal(err)
	}
	got := interestingReasons(t, s, "t1")
	if !got["error"] || !got["anomaly"] {
		t.Fatalf("want both error and anomaly rows, got %v", got)
	}
	if n := s.StatsForVariant("cumulative:50").ErrorCount; n != 1 {
		t.Fatalf("error_count = %d, want 1", n)
	}
	if got, err := s.GetInterestingTrace("t1"); err != nil || got == nil {
		t.Fatalf("GetInterestingTrace: %v %v", got, err)
	}
}

// Databases created before the composite key keep their rows and gain the new key.
func TestInterestingTraces_MigratesLegacyPrimaryKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE interesting_traces (
		trace_id TEXT PRIMARY KEY, service_id TEXT NOT NULL, fingerprint TEXT NOT NULL, reason TEXT NOT NULL,
		score REAL NOT NULL, summary TEXT, spans_json TEXT NOT NULL, span_count INTEGER NOT NULL,
		duration_us INTEGER NOT NULL, has_error BOOLEAN NOT NULL, created_at TIMESTAMP NOT NULL);
		INSERT INTO interesting_traces VALUES ('old', 'svc', 'fp', 'error', 0, 's', '[]', 1, 10, 1, '2026-01-01 00:00:00');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	s := openTestStore(t, path)
	if got := interestingReasons(t, s, "old"); !got["error"] {
		t.Fatalf("legacy row lost in migration: %v", got)
	}
	tr := sampleTrace("old", time.Now())
	if err := s.StoreInterestingTrace(tr, "deviation", 0.4, "d", "cumulative:50"); err != nil {
		t.Fatal(err)
	}
	if got := interestingReasons(t, s, "old"); !got["error"] || !got["deviation"] {
		t.Fatalf("want error and deviation after migration, got %v", got)
	}
}

func TestCleanup_RemovesOldInterestingTraces(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "t.db"))
	old := sampleTrace("old", time.Now().Add(-3*time.Hour))
	fresh := sampleTrace("fresh", time.Now())
	s.StoreInterestingTrace(old, "error", 0, "", "")
	s.StoreInterestingTrace(fresh, "error", 0, "", "")

	// 15 minute retention keeps important data 10x longer: 150 minutes.
	if err := s.Cleanup(15 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := interestingReasons(t, s, "old"); len(got) != 0 {
		t.Errorf("interesting trace older than important retention survived: %v", got)
	}
	if got := interestingReasons(t, s, "fresh"); !got["error"] {
		t.Errorf("fresh interesting trace deleted")
	}
}

// CURRENT_TIMESTAMP is UTC; cutoffs built from local time must not delete fresh rows.
func TestCleanup_UsesUTCCutoffs(t *testing.T) {
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	orig := time.Local
	time.Local = kolkata
	t.Cleanup(func() { time.Local = orig })

	s := openTestStore(t, filepath.Join(t.TempDir(), "t.db"))
	tr := sampleTrace("fresh", time.Now())
	tr.HasError = false
	s.StoreTrace(tr)
	s.StoreDeviation(&models.Deviation{TraceID: "fresh", ServiceID: "svc", Score: 1}, "cumulative:50")
	s.StoreInterestingTrace(tr, "deviation", 1, "", "cumulative:50")

	if err := s.Cleanup(15 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTrace("fresh"); got == nil {
		t.Error("fresh trace deleted by cleanup")
	}
	if got, _ := s.GetDeviations("svc", "", 10, 0); len(got) != 1 {
		t.Errorf("fresh deviation deleted by cleanup: %d rows", len(got))
	}
	if got := interestingReasons(t, s, "fresh"); !got["deviation"] {
		t.Error("fresh interesting trace deleted by cleanup")
	}
}

func TestIssueQueries_FilterByVariant(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "t.db"))
	for _, v := range []string{"cumulative:50", "ewma:500"} {
		s.StoreDeviation(&models.Deviation{TraceID: "t", ServiceID: "svc", Score: 1}, v)
		s.StoreAnomaly(&models.Anomaly{TraceID: "t", ServiceID: "svc", Score: 5}, v)
	}
	if got, _ := s.GetDeviations("svc", "ewma:500", 10, 0); len(got) != 1 {
		t.Errorf("deviations for one variant: %d, want 1", len(got))
	}
	if got, _ := s.GetDeviations("svc", "", 10, 0); len(got) != 2 {
		t.Errorf("deviations unfiltered: %d, want 2", len(got))
	}
	if got, _ := s.GetAnomalies("svc", "ewma:500", 10, 0); len(got) != 1 {
		t.Errorf("anomalies for one variant: %d, want 1", len(got))
	}
	counts, err := s.GetServiceIssueCounts("ewma:500")
	if err != nil {
		t.Fatal(err)
	}
	if c := counts["svc"]; c.DeviationCount != 1 || c.AnomalyCount != 1 {
		t.Errorf("issue counts for one variant: %+v", c)
	}
}

func TestCompaction_StopFlushesOpenWindow(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "t.db"))
	c := NewCompactionService(s, nil, time.Hour, time.Hour, time.Hour)
	c.Start()
	c.AddToRollup(sampleTrace("t", time.Now()), "fp")
	c.Stop()

	rollups, err := s.GetRollupsBeforeCutoff(time.Now().Add(2 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rollups) != 1 || rollups[0].TraceCount != 1 {
		t.Fatalf("in-progress window not flushed on stop: %d rollups", len(rollups))
	}
}

// A restart splits one window across two flushes; the second must add to the
// first instead of replacing it.
func TestStoreRollup_MergesSameWindow(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "t.db"))
	start := time.Now().Add(-time.Hour).Truncate(15 * time.Minute)
	end := start.Add(15 * time.Minute)
	before := &models.Rollup{
		WindowStart: start, WindowEnd: end, ServiceID: "svc", Fingerprint: "fp",
		TraceCount: 30, SpanCount: 90, ErrorCount: 3, DurationSum: 3000,
		DurationMin: 15, DurationMax: 200, DurationP50: 100, DurationP99: 190,
		BranchStats: map[string]*models.BranchStats{
			"a": {Count: 30, SumDuration: 900, MinDuration: 10, MaxDuration: 60, ErrorCount: 1},
		},
		SampleTraceIDs: []string{"t1", "t2"},
	}
	after := &models.Rollup{
		WindowStart: start, WindowEnd: end, ServiceID: "svc", Fingerprint: "fp",
		TraceCount: 10, SpanCount: 20, ErrorCount: 1, DurationSum: 2000,
		DurationMin: 20, DurationMax: 400, DurationP50: 200, DurationP99: 390,
		BranchStats: map[string]*models.BranchStats{
			"a": {Count: 10, SumDuration: 500, MinDuration: 5, MaxDuration: 90, ErrorCount: 2},
			"b": {Count: 10, SumDuration: 100, MinDuration: 8, MaxDuration: 12},
		},
		SampleTraceIDs: []string{"t2", "t3"},
	}
	if err := s.StoreRollup(before); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreRollup(after); err != nil {
		t.Fatal(err)
	}

	rollups, err := s.GetRollupsBeforeCutoff(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rollups) != 1 {
		t.Fatalf("expected 1 merged rollup, got %d", len(rollups))
	}
	r := rollups[0]
	if r.TraceCount != 40 || r.SpanCount != 110 || r.ErrorCount != 4 || r.DurationSum != 5000 {
		t.Errorf("counts not added: %+v", r)
	}
	if r.DurationMin != 15 || r.DurationMax != 400 {
		t.Errorf("min/max not combined: min=%d max=%d", r.DurationMin, r.DurationMax)
	}
	if r.DurationP50 != 125 || r.DurationP99 != 240 {
		t.Errorf("percentiles not trace-count weighted: p50=%d p99=%d", r.DurationP50, r.DurationP99)
	}
	a, b := r.BranchStats["a"], r.BranchStats["b"]
	if a == nil || *a != (models.BranchStats{Count: 40, SumDuration: 1400, MinDuration: 5, MaxDuration: 90, ErrorCount: 3}) {
		t.Errorf("branch a not merged: %+v", a)
	}
	if b == nil || b.Count != 10 {
		t.Errorf("branch b lost: %+v", b)
	}
	if strings.Join(r.SampleTraceIDs, ",") != "t1,t2,t3" {
		t.Errorf("sample ids not unioned: %v", r.SampleTraceIDs)
	}
}
