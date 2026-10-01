package storage

import (
	"os"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/models"
)

func TestSQLiteStorage_TraceOperations(t *testing.T) {
	tmpFile := "/tmp/test_traces_" + time.Now().Format("20060102150405") + ".db"
	defer os.Remove(tmpFile)

	storage, err := NewSQLiteStorage(tmpFile, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer storage.Close()

	t.Run("store and retrieve trace", func(t *testing.T) {
		trace := &models.Trace{
			TraceID:         "trace-123",
			ServiceID:       "api:GET /users",
			Fingerprint:     "abc123",
			TotalDurationUs: 50000,
			HasError:        false,
			Spans: []*models.Span{
				{
					SpanID:        "span-1",
					ParentID:      "",
					ServiceName:   "api",
					OperationName: "GET /users",
					StartTime:     time.Now().UnixMicro(),
					Duration:      50000,
					Tags:          map[string]string{"http.status_code": "200"},
				},
			},
		}

		err := storage.StoreTrace(trace)
		if err != nil {
			t.Fatalf("failed to store trace: %v", err)
		}

		retrieved, err := storage.GetTrace("trace-123")
		if err != nil {
			t.Fatalf("failed to get trace: %v", err)
		}

		if retrieved == nil {
			t.Fatal("trace not found")
		}

		if retrieved.TraceID != "trace-123" {
			t.Errorf("expected trace-123, got %s", retrieved.TraceID)
		}

		if retrieved.ServiceID != "api:GET /users" {
			t.Errorf("expected api:GET /users, got %s", retrieved.ServiceID)
		}

		if retrieved.TotalDurationUs != 50000 {
			t.Errorf("expected 50000, got %d", retrieved.TotalDurationUs)
		}

		if len(retrieved.Spans) != 1 {
			t.Errorf("expected 1 span, got %d", len(retrieved.Spans))
		}
	})

	t.Run("get non-existent trace", func(t *testing.T) {
		trace, err := storage.GetTrace("non-existent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if trace != nil {
			t.Error("expected nil for non-existent trace")
		}
	})

	t.Run("store and retrieve deviation", func(t *testing.T) {
		deviation := &models.Deviation{
			TraceID:              "trace-456",
			ServiceID:            "api:GET /users",
			Fingerprint:          "xyz789",
			CanonicalFingerprint: "abc123",
			Score:                0.45,
			DiffSummary:          "Added: db:query",
		}

		err := storage.StoreDeviation(deviation, "")
		if err != nil {
			t.Fatalf("failed to store deviation: %v", err)
		}

		deviations, err := storage.GetDeviations("api:GET /users", "", 10, 0.0)
		if err != nil {
			t.Fatalf("failed to get deviations: %v", err)
		}

		if len(deviations) != 1 {
			t.Errorf("expected 1 deviation, got %d", len(deviations))
		}
	})

	t.Run("stats", func(t *testing.T) {
		stats := storage.Stats()
		if stats.TraceCount < 1 {
			t.Error("expected at least 1 trace")
		}
		if stats.DeviationCount < 1 {
			t.Error("expected at least 1 deviation")
		}
	})
}

// createTestStorage is a test helper that creates a temporary SQLite storage
func createTestStorage(t *testing.T) (*SQLiteStorage, func()) {
	t.Helper()
	tmpFile := "/tmp/test_storage_" + time.Now().Format("20060102150405.000000") + ".db"
	storage, err := NewSQLiteStorage(tmpFile, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	cleanup := func() {
		storage.Close()
		os.Remove(tmpFile)
	}
	return storage, cleanup
}

func TestStoreDeviationWithVariant(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	deviation := &models.Deviation{
		TraceID:              "trace-1",
		ServiceID:            "service-1",
		Fingerprint:          "fp-1",
		CanonicalFingerprint: "canonical-fp-1",
		Score:                0.8,
		DiffSummary:          "Test deviation",
	}

	// Store with variant
	err := storage.StoreDeviation(deviation, "cumulative:50")
	if err != nil {
		t.Fatalf("failed to store deviation with cumulative:50 variant: %v", err)
	}

	// Store same trace with different variant
	err = storage.StoreDeviation(deviation, "ewma:100")
	if err != nil {
		t.Fatalf("failed to store deviation with ewma:100 variant: %v", err)
	}

	// Verify both stored - check per-variant stats
	stats := storage.StatsForVariant("cumulative:50")
	if stats.DeviationCount != 1 {
		t.Errorf("expected 1 deviation for cumulative:50, got %d", stats.DeviationCount)
	}

	stats = storage.StatsForVariant("ewma:100")
	if stats.DeviationCount != 1 {
		t.Errorf("expected 1 deviation for ewma:100, got %d", stats.DeviationCount)
	}

	// Total should be 2
	stats = storage.Stats()
	if stats.DeviationCount != 2 {
		t.Errorf("expected 2 total deviations, got %d", stats.DeviationCount)
	}
}

func TestStoreAnomalyWithVariant(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	anomaly := &models.Anomaly{
		TraceID:      "trace-1",
		ServiceID:    "service-1",
		Fingerprint:  "fp-1",
		Score:        4.5,
		SlowBranches: []string{"db:query", "cache:lookup"},
	}

	// Store with variant
	err := storage.StoreAnomaly(anomaly, "cumulative:50")
	if err != nil {
		t.Fatalf("failed to store anomaly with cumulative:50 variant: %v", err)
	}

	// Store same trace with different variant
	err = storage.StoreAnomaly(anomaly, "ewma:100")
	if err != nil {
		t.Fatalf("failed to store anomaly with ewma:100 variant: %v", err)
	}

	// Verify both stored - check per-variant stats
	stats := storage.StatsForVariant("cumulative:50")
	if stats.AnomalyCount != 1 {
		t.Errorf("expected 1 anomaly for cumulative:50, got %d", stats.AnomalyCount)
	}

	stats = storage.StatsForVariant("ewma:100")
	if stats.AnomalyCount != 1 {
		t.Errorf("expected 1 anomaly for ewma:100, got %d", stats.AnomalyCount)
	}

	// Total should be 2
	stats = storage.Stats()
	if stats.AnomalyCount != 2 {
		t.Errorf("expected 2 total anomalies, got %d", stats.AnomalyCount)
	}
}

func TestStatsForVariant(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	// Store anomalies with different variants
	anomaly1 := &models.Anomaly{TraceID: "t1", ServiceID: "s1", Fingerprint: "fp1", Score: 4.0}
	anomaly2 := &models.Anomaly{TraceID: "t2", ServiceID: "s1", Fingerprint: "fp2", Score: 4.0}
	anomaly3 := &models.Anomaly{TraceID: "t3", ServiceID: "s1", Fingerprint: "fp3", Score: 4.0}

	if err := storage.StoreAnomaly(anomaly1, "cumulative:50"); err != nil {
		t.Fatalf("failed to store anomaly1: %v", err)
	}
	if err := storage.StoreAnomaly(anomaly2, "cumulative:50"); err != nil {
		t.Fatalf("failed to store anomaly2: %v", err)
	}
	if err := storage.StoreAnomaly(anomaly3, "ewma:100"); err != nil {
		t.Fatalf("failed to store anomaly3: %v", err)
	}

	// Store deviations with different variants
	deviation1 := &models.Deviation{TraceID: "t4", ServiceID: "s1", Fingerprint: "fp4", CanonicalFingerprint: "cfp", Score: 0.5}
	deviation2 := &models.Deviation{TraceID: "t5", ServiceID: "s1", Fingerprint: "fp5", CanonicalFingerprint: "cfp", Score: 0.6}
	deviation3 := &models.Deviation{TraceID: "t6", ServiceID: "s1", Fingerprint: "fp6", CanonicalFingerprint: "cfp", Score: 0.7}

	if err := storage.StoreDeviation(deviation1, "cumulative:50"); err != nil {
		t.Fatalf("failed to store deviation1: %v", err)
	}
	if err := storage.StoreDeviation(deviation2, "ewma:100"); err != nil {
		t.Fatalf("failed to store deviation2: %v", err)
	}
	if err := storage.StoreDeviation(deviation3, "ewma:100"); err != nil {
		t.Fatalf("failed to store deviation3: %v", err)
	}

	// Verify filtering - cumulative:50
	stats := storage.StatsForVariant("cumulative:50")
	if stats.AnomalyCount != 2 {
		t.Errorf("expected 2 anomalies for cumulative:50, got %d", stats.AnomalyCount)
	}
	if stats.DeviationCount != 1 {
		t.Errorf("expected 1 deviation for cumulative:50, got %d", stats.DeviationCount)
	}

	// Verify filtering - ewma:100
	stats = storage.StatsForVariant("ewma:100")
	if stats.AnomalyCount != 1 {
		t.Errorf("expected 1 anomaly for ewma:100, got %d", stats.AnomalyCount)
	}
	if stats.DeviationCount != 2 {
		t.Errorf("expected 2 deviations for ewma:100, got %d", stats.DeviationCount)
	}

	// Verify total counts
	stats = storage.Stats()
	if stats.AnomalyCount != 3 {
		t.Errorf("expected 3 total anomalies, got %d", stats.AnomalyCount)
	}
	if stats.DeviationCount != 3 {
		t.Errorf("expected 3 total deviations, got %d", stats.DeviationCount)
	}

	// Verify non-existent variant returns zeros
	stats = storage.StatsForVariant("nonexistent:variant")
	if stats.AnomalyCount != 0 {
		t.Errorf("expected 0 anomalies for nonexistent variant, got %d", stats.AnomalyCount)
	}
	if stats.DeviationCount != 0 {
		t.Errorf("expected 0 deviations for nonexistent variant, got %d", stats.DeviationCount)
	}
}

func TestStatsForVariant_EmptyVariant(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	// Store with empty variant (legacy behavior)
	anomaly := &models.Anomaly{TraceID: "t1", ServiceID: "s1", Fingerprint: "fp1", Score: 4.0}
	if err := storage.StoreAnomaly(anomaly, ""); err != nil {
		t.Fatalf("failed to store anomaly: %v", err)
	}

	deviation := &models.Deviation{TraceID: "t2", ServiceID: "s1", Fingerprint: "fp2", CanonicalFingerprint: "cfp", Score: 0.5}
	if err := storage.StoreDeviation(deviation, ""); err != nil {
		t.Fatalf("failed to store deviation: %v", err)
	}

	// Verify empty string variant filtering works
	stats := storage.StatsForVariant("")
	if stats.AnomalyCount != 1 {
		t.Errorf("expected 1 anomaly for empty variant, got %d", stats.AnomalyCount)
	}
	if stats.DeviationCount != 1 {
		t.Errorf("expected 1 deviation for empty variant, got %d", stats.DeviationCount)
	}

	// Verify specific variant doesn't see empty variant records
	stats = storage.StatsForVariant("cumulative:50")
	if stats.AnomalyCount != 0 {
		t.Errorf("expected 0 anomalies for cumulative:50, got %d", stats.AnomalyCount)
	}
	if stats.DeviationCount != 0 {
		t.Errorf("expected 0 deviations for cumulative:50, got %d", stats.DeviationCount)
	}
}

func TestSQLiteStorage_RollupOperations(t *testing.T) {
	tmpFile := "/tmp/test_rollups_" + time.Now().Format("20060102150405") + ".db"
	defer os.Remove(tmpFile)

	storage, err := NewSQLiteStorage(tmpFile, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer storage.Close()

	now := time.Now()
	rollup := &models.Rollup{
		WindowStart: now.Add(-30 * time.Minute),
		WindowEnd:   now.Add(-15 * time.Minute),
		ServiceID:   "api:GET /users",
		Fingerprint: "abc123",
		TraceCount:  100,
		SpanCount:   500,
		ErrorCount:  5,
		DurationSum: 5000000,
		DurationMin: 10000,
		DurationMax: 100000,
		DurationP50: 45000,
		DurationP99: 95000,
		BranchStats: map[string]*models.BranchStats{
			"api:handler": {Count: 100, SumDuration: 2000000},
		},
		SampleTraceIDs: []string{"t1", "t2", "t3"},
	}

	err = storage.StoreRollup(rollup)
	if err != nil {
		t.Fatalf("failed to store rollup: %v", err)
	}

	t.Run("get rollups before cutoff", func(t *testing.T) {
		rollups, err := storage.GetRollupsBeforeCutoff(now)
		if err != nil {
			t.Fatalf("failed to get rollups: %v", err)
		}

		if len(rollups) != 1 {
			t.Errorf("expected 1 rollup, got %d", len(rollups))
		}
	})

	t.Run("delete rollups before cutoff", func(t *testing.T) {
		deleted, err := storage.DeleteRollupsBeforeCutoff(now)
		if err != nil {
			t.Fatalf("failed to delete rollups: %v", err)
		}

		if deleted != 1 {
			t.Errorf("expected 1 deleted, got %d", deleted)
		}

		rollups, _ := storage.GetRollupsBeforeCutoff(now)
		if len(rollups) != 0 {
			t.Error("expected 0 rollups after deletion")
		}
	})
}

func TestStatsForVariant_IncludesTraceAndSpanCounts(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	trace := &models.Trace{
		TraceID:     "t1",
		ServiceID:   "s1",
		Fingerprint: "fp1",
		Spans: []*models.Span{
			{SpanID: "a", ServiceName: "api", OperationName: "GET /", StartTime: time.Now().UnixMicro(), Duration: 100},
			{SpanID: "b", ParentID: "a", ServiceName: "db", OperationName: "query", StartTime: time.Now().UnixMicro(), Duration: 50},
		},
	}
	if err := storage.StoreTrace(trace); err != nil {
		t.Fatalf("failed to store trace: %v", err)
	}

	total := storage.Stats()
	stats := storage.StatsForVariant("cumulative:50")
	if stats.TraceCount != total.TraceCount || stats.TraceCount == 0 {
		t.Errorf("expected TraceCount %d, got %d", total.TraceCount, stats.TraceCount)
	}
	if stats.SpanCount != total.SpanCount || stats.SpanCount == 0 {
		t.Errorf("expected SpanCount %d, got %d", total.SpanCount, stats.SpanCount)
	}
}

func TestDistinctInterestingTracesCount_FiltersByVariant(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	trace := func(id string) *models.Trace {
		return &models.Trace{TraceID: id, ServiceID: "s1", Fingerprint: "fp", Spans: []*models.Span{
			{SpanID: "a", ServiceName: "api", OperationName: "GET /", StartTime: time.Now().UnixMicro(), Duration: 100},
		}}
	}
	for _, tc := range []struct{ id, reason, variant string }{
		{"t1", "error", "cumulative:50"},
		{"t1", "anomaly", "cumulative:50"},
		{"t2", "error", "ewma:100"},
	} {
		if err := storage.StoreInterestingTrace(trace(tc.id), tc.reason, 1, "", tc.variant); err != nil {
			t.Fatalf("store %s/%s: %v", tc.id, tc.reason, err)
		}
	}

	for variant, want := range map[string]int64{"cumulative:50": 1, "ewma:100": 1, "": 2} {
		got, err := storage.GetDistinctInterestingTracesCount(variant)
		if err != nil {
			t.Fatalf("count %q: %v", variant, err)
		}
		if got != want {
			t.Errorf("variant %q: expected %d distinct traces, got %d", variant, want, got)
		}
	}
}
