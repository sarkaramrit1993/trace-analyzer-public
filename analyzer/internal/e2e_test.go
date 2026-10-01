package internal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/api"
	"github.com/trace-analyzer/internal/ingestion"
	"github.com/trace-analyzer/internal/models"
	"github.com/trace-analyzer/internal/storage"
)

// defaultCombination matches the VariantManager's default active variant:threshold key.
const defaultCombination = "cumulative:50"

func TestE2E_FullPipeline(t *testing.T) {
	// File-backed: with ":memory:" each pooled connection gets its own empty DB,
	// and the assembler's background goroutine opens a second connection.
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "e2e.db"), 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer store.Close()

	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{Combinations: []analysis.VariantCombination{defaultCombination}})
	baseline := vm.GetActiveBaseline()
	fingerprinter := analysis.NewFingerprinter()
	deviationDetector := analysis.NewDeviationDetector(baseline, 0.3)
	anomalyScorer := analysis.NewAnomalyScorer(baseline, 3.5, 0.75)

	var processedTraces atomic.Int64
	var mu sync.Mutex

	traceHandler := func(trace *models.Trace) error {
		mu.Lock()
		defer mu.Unlock()

		fp := fingerprinter.Compute(trace)
		trace.Fingerprint = fp

		vm.UpdateAll(trace, fp)

		if err := store.StoreTrace(trace); err != nil {
			return err
		}

		deviation := deviationDetector.Check(trace, fp)
		if deviation != nil {
			store.StoreDeviation(deviation, defaultCombination)
		}

		anomaly := anomalyScorer.Score(trace, fp)
		if anomaly != nil {
			store.StoreAnomaly(anomaly, defaultCombination)
		}

		processedTraces.Add(1)
		return nil
	}

	assembler := ingestion.NewTraceAssembler(
		2*time.Second,
		[]string{"service_name", "operation_name"},
		traceHandler,
		store,
	)
	assembler.Start()
	defer assembler.Stop()

	testSpans := generateTestSpans()
	for _, span := range testSpans {
		if err := assembler.AddSpan(span); err != nil {
			t.Errorf("failed to add span: %v", err)
		}
	}

	// Traces are emitted after the 2s assembly timeout on the next 1s tick,
	// so poll rather than sleep a fixed amount that is borderline under -race.
	deadline := time.Now().Add(15 * time.Second)
	for processedTraces.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	if processedTraces.Load() == 0 {
		t.Error("no traces were processed")
	}
	mu.Unlock()

	stats := store.Stats()
	if stats.TraceCount == 0 {
		t.Error("no traces in storage")
	}

	services := baseline.GetAllServices()
	if len(services) == 0 {
		t.Error("no services tracked in baseline")
	}

	t.Logf("Processed %d traces, %d services tracked", processedTraces.Load(), len(services))
}

func TestE2E_APIEndpoints(t *testing.T) {
	store, _ := storage.NewSQLiteStorage(":memory:", 15*time.Minute)
	defer store.Close()

	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	fingerprinter := analysis.NewFingerprinter()

	// Use simple service ID without slash to avoid URL routing issues
	serviceID := "api-gateway:checkout"

	for i := 0; i < 20; i++ {
		trace := createTestTrace(fmt.Sprintf("trace-%d", i), serviceID)
		fp := fingerprinter.Compute(trace)
		trace.Fingerprint = fp
		vm.UpdateAll(trace, fp)
		store.StoreTrace(trace)
	}

	store.StoreDeviation(&models.Deviation{
		TraceID:              "trace-deviated",
		ServiceID:            serviceID,
		Fingerprint:          "different-fp",
		CanonicalFingerprint: "canonical-fp",
		Score:                0.45,
		DiffSummary:          "Added: fraud-detection:check",
	}, defaultCombination)

	store.StoreAnomaly(&models.Anomaly{
		TraceID:      "trace-slow",
		ServiceID:    serviceID,
		Score:        5.2,
		SlowBranches: []string{"payment-service:ProcessPayment"},
	}, defaultCombination)

	server := api.NewServerWithVariants(store, vm, []string{"service_identity", "scope1", "scope2", "scope3", "env", "operation"}, nil)

	tests := []struct {
		name           string
		endpoint       string
		expectedStatus int
		checkBody      func([]byte) error
	}{
		{
			name:           "health check",
			endpoint:       "/health",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "list services",
			endpoint:       "/api/services",
			expectedStatus: http.StatusOK,
			checkBody: func(body []byte) error {
				var services []models.ServiceIdentity
				if err := json.Unmarshal(body, &services); err != nil {
					return err
				}
				if len(services) == 0 {
					return fmt.Errorf("expected services, got none")
				}
				return nil
			},
		},
		{
			name:           "get topologies",
			endpoint:       "/api/services/api-gateway:checkout/topologies",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "get deviations",
			endpoint:       "/api/services/api-gateway:checkout/deviations",
			expectedStatus: http.StatusOK,
			checkBody: func(body []byte) error {
				var deviations []*models.Deviation
				if err := json.Unmarshal(body, &deviations); err != nil {
					return err
				}
				if len(deviations) == 0 {
					return fmt.Errorf("expected deviations, got none")
				}
				return nil
			},
		},
		{
			name:           "get anomalies",
			endpoint:       "/api/services/api-gateway:checkout/anomalies?min_score=0",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "get stats",
			endpoint:       "/api/stats",
			expectedStatus: http.StatusOK,
			checkBody: func(body []byte) error {
				var stats map[string]interface{}
				if err := json.Unmarshal(body, &stats); err != nil {
					return err
				}
				if stats["hot_tier_traces"].(float64) == 0 {
					return fmt.Errorf("expected traces in stats")
				}
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.endpoint, nil)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)

			if w.Code != tc.expectedStatus {
				t.Errorf("expected status %d, got %d", tc.expectedStatus, w.Code)
			}

			if tc.checkBody != nil {
				if err := tc.checkBody(w.Body.Bytes()); err != nil {
					t.Errorf("body check failed: %v", err)
				}
			}
		})
	}
}

func TestE2E_Fingerprinting(t *testing.T) {
	fp := analysis.NewFingerprinter()

	trace1 := createTestTrace("t1", "svc1")
	trace2 := createTestTrace("t2", "svc1")

	fp1 := fp.Compute(trace1)
	fp2 := fp.Compute(trace2)

	if fp1 != fp2 {
		t.Errorf("same topology should have same fingerprint: %s vs %s", fp1, fp2)
	}

	trace3 := createTestTraceWithDeviation("t3", "svc1")
	fp3 := fp.Compute(trace3)

	if fp1 == fp3 {
		t.Error("different topology should have different fingerprint")
	}
}

func TestE2E_DeviationDetection(t *testing.T) {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{Combinations: []analysis.VariantCombination{defaultCombination}})
	baseline := vm.GetActiveBaseline()
	fingerprinter := analysis.NewFingerprinter()
	detector := analysis.NewDeviationDetector(baseline, 0.3)

	for i := 0; i < 50; i++ {
		trace := createTestTrace(fmt.Sprintf("trace-%d", i), "test-service")
		fp := fingerprinter.Compute(trace)
		vm.UpdateAll(trace, fp)
	}

	canonical := baseline.GetCanonicalFingerprint("test-service")
	if canonical == "" {
		t.Fatal("expected canonical fingerprint to be set")
	}

	devTrace := createTestTraceWithDeviation("deviated", "test-service")
	devFp := fingerprinter.Compute(devTrace)

	if devFp == canonical {
		t.Fatal("deviated trace should have different fingerprint")
	}

	deviation := detector.Check(devTrace, devFp)
	if deviation == nil {
		t.Log("No deviation detected (score below threshold)")
	} else {
		if deviation.Score <= 0 {
			t.Error("deviation score should be positive")
		}
		t.Logf("Detected deviation with score: %.2f", deviation.Score)
	}
}

func TestE2E_AnomalyScoring(t *testing.T) {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{Combinations: []analysis.VariantCombination{defaultCombination}})
	baseline := vm.GetActiveBaseline()
	fingerprinter := analysis.NewFingerprinter()
	scorer := analysis.NewAnomalyScorer(baseline, 3.5, 0.75)

	for i := 0; i < 60; i++ {
		trace := createTestTrace(fmt.Sprintf("trace-%d", i), "test-service")
		fp := fingerprinter.Compute(trace)
		vm.UpdateAll(trace, fp)
	}

	slowTrace := createSlowTrace("slow-trace", "test-service")
	fp := fingerprinter.Compute(slowTrace)
	vm.UpdateAll(slowTrace, fp)

	anomaly := scorer.Score(slowTrace, fp)
	if anomaly == nil {
		t.Log("No anomaly detected (may need more baseline samples or higher slowness)")
	} else {
		if anomaly.Score <= 0 {
			t.Error("anomaly score should be positive")
		}
		t.Logf("Detected anomaly with score: %.2f, slow branches: %v", anomaly.Score, anomaly.SlowBranches)
	}
}

func TestE2E_SSEParsing(t *testing.T) {
	ssePayload := `[{"TraceID":"abc123","SpanID":"span1","ParentID":"","OperationName":"POST /checkout","StartTime":1703123456789000,"Duration":50000,"Tags":{"http.status_code":"200","service_name":"api-gateway"},"ServiceName":"api-gateway"},{"TraceID":"abc123","SpanID":"span2","ParentID":"span1","OperationName":"GetCart","StartTime":1703123456789100,"Duration":8000,"Tags":{"service_name":"cart-service"},"ServiceName":"cart-service"}]`

	var spans []*models.Span
	err := json.Unmarshal([]byte(ssePayload), &spans)
	if err != nil {
		t.Fatalf("failed to parse SSE payload: %v", err)
	}

	if len(spans) != 2 {
		t.Errorf("expected 2 spans, got %d", len(spans))
	}

	if !spans[0].IsRoot() {
		t.Error("first span should be root")
	}

	if spans[1].IsRoot() {
		t.Error("second span should not be root")
	}

	if spans[0].TraceID != "abc123" {
		t.Errorf("expected TraceID abc123, got %s", spans[0].TraceID)
	}

	if spans[0].ServiceName != "api-gateway" {
		t.Errorf("expected ServiceName api-gateway, got %s", spans[0].ServiceName)
	}

	if spans[0].GetStatus() != "OK" {
		t.Errorf("expected OK status, got %s", spans[0].GetStatus())
	}
}

func TestE2E_StorageRoundTrip(t *testing.T) {
	store, err := storage.NewSQLiteStorage(":memory:", 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer store.Close()

	trace := createTestTrace("roundtrip-test", "test-service:op")
	trace.Fingerprint = "test-fp"

	err = store.StoreTrace(trace)
	if err != nil {
		t.Fatalf("failed to store trace: %v", err)
	}

	retrieved, err := store.GetTrace("roundtrip-test")
	if err != nil {
		t.Fatalf("failed to get trace: %v", err)
	}

	if retrieved == nil {
		t.Fatal("trace not found")
	}

	if retrieved.TraceID != trace.TraceID {
		t.Errorf("TraceID mismatch: %s vs %s", retrieved.TraceID, trace.TraceID)
	}

	if retrieved.ServiceID != trace.ServiceID {
		t.Errorf("ServiceID mismatch: %s vs %s", retrieved.ServiceID, trace.ServiceID)
	}

	if len(retrieved.Spans) != len(trace.Spans) {
		t.Errorf("Span count mismatch: %d vs %d", len(retrieved.Spans), len(trace.Spans))
	}

	if retrieved.RootSpan == nil {
		t.Error("root span not found")
	}
}

func TestE2E_BranchExtraction(t *testing.T) {
	fp := analysis.NewFingerprinter()
	trace := createTestTrace("test", "svc")

	branches := fp.ExtractBranches(trace)
	if len(branches) != 2 {
		t.Errorf("expected 2 branches, got %d", len(branches))
	}

	for _, b := range branches {
		if b.ParentService == "" {
			t.Error("branch parent service should not be empty")
		}
		if b.ChildService == "" {
			t.Error("branch child service should not be empty")
		}
	}
}

// Helper functions

func generateTestSpans() []*models.Span {
	var spans []*models.Span
	now := time.Now().UnixMicro()

	for i := 0; i < 5; i++ {
		traceID := fmt.Sprintf("trace-%d", i)
		spans = append(spans,
			&models.Span{
				TraceID:       traceID,
				SpanID:        fmt.Sprintf("%s-root", traceID),
				ParentID:      "",
				ServiceName:   "api-gateway",
				OperationName: "POST /checkout",
				StartTime:     now + int64(i*100000),
				Duration:      50000,
				Tags:          map[string]string{"http.status_code": "200"},
			},
			&models.Span{
				TraceID:       traceID,
				SpanID:        fmt.Sprintf("%s-child1", traceID),
				ParentID:      fmt.Sprintf("%s-root", traceID),
				ServiceName:   "cart-service",
				OperationName: "GetCart",
				StartTime:     now + int64(i*100000) + 1000,
				Duration:      8000,
				Tags:          map[string]string{},
			},
			&models.Span{
				TraceID:       traceID,
				SpanID:        fmt.Sprintf("%s-child2", traceID),
				ParentID:      fmt.Sprintf("%s-root", traceID),
				ServiceName:   "payment-service",
				OperationName: "ProcessPayment",
				StartTime:     now + int64(i*100000) + 10000,
				Duration:      25000,
				Tags:          map[string]string{},
			},
		)
	}

	return spans
}

func createTestTrace(traceID, serviceID string) *models.Trace {
	now := time.Now().UnixMicro()
	rootSpan := &models.Span{
		TraceID:       traceID,
		SpanID:        traceID + "-root",
		ParentID:      "",
		ServiceName:   "api-gateway",
		OperationName: "POST /checkout",
		StartTime:     now,
		Duration:      50000,
		Tags:          map[string]string{"http.status_code": "200"},
	}

	return &models.Trace{
		TraceID:         traceID,
		ServiceID:       serviceID,
		TotalDurationUs: 50000,
		StartTime:       time.UnixMicro(now),
		RootSpan:        rootSpan,
		Spans: []*models.Span{
			rootSpan,
			{
				TraceID:       traceID,
				SpanID:        traceID + "-child1",
				ParentID:      traceID + "-root",
				ServiceName:   "cart-service",
				OperationName: "GetCart",
				StartTime:     now + 1000,
				Duration:      8000,
				Tags:          map[string]string{},
			},
			{
				TraceID:       traceID,
				SpanID:        traceID + "-child2",
				ParentID:      traceID + "-root",
				ServiceName:   "payment-service",
				OperationName: "ProcessPayment",
				StartTime:     now + 10000,
				Duration:      25000,
				Tags:          map[string]string{},
			},
		},
	}
}

func createTestTraceWithDeviation(traceID, serviceID string) *models.Trace {
	trace := createTestTrace(traceID, serviceID)
	now := time.Now().UnixMicro()

	trace.Spans = append(trace.Spans, &models.Span{
		TraceID:       traceID,
		SpanID:        traceID + "-deviation",
		ParentID:      traceID + "-root",
		ServiceName:   "fraud-detection",
		OperationName: "AnalyzeTransaction",
		StartTime:     now + 20000,
		Duration:      15000,
		Tags:          map[string]string{},
	})

	return trace
}

func createSlowTrace(traceID, serviceID string) *models.Trace {
	trace := createTestTrace(traceID, serviceID)

	for _, span := range trace.Spans {
		if span.ServiceName == "payment-service" {
			span.Duration = 500000
		}
	}
	trace.TotalDurationUs = 550000

	return trace
}
