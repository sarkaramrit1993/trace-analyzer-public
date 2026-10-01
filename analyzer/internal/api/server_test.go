package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/models"
	"github.com/trace-analyzer/internal/storage"
)

const testSvc = "svc-a"

func testTrace(id, op string) *models.Trace {
	now := time.Now()
	root := &models.Span{TraceID: id, SpanID: id + "-r", ServiceName: "gw", OperationName: "GET /", StartTime: now.UnixMicro(), Duration: 20000}
	child := &models.Span{TraceID: id, SpanID: id + "-c", ParentID: root.SpanID, ServiceName: op, OperationName: op, StartTime: now.UnixMicro() + 10, Duration: 5000}
	return &models.Trace{TraceID: id, ServiceID: testSvc, StartTime: now, TotalDurationUs: 20000, RootSpan: root, Spans: []*models.Span{root, child}}
}

func newTestStore(t *testing.T) *storage.SQLiteStorage {
	t.Helper()
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "traces.db"), 15*time.Minute)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// Handlers must read baselines through snapshots, never the live maps that
// UpdateAll writes under the computer's lock.
func TestServer_BaselineHandlersRaceFreeWithUpdates(t *testing.T) {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	fp := analysis.NewFingerprinter()
	first := testTrace("t-0", "op-0")
	firstFP := fp.Compute(first)
	vm.UpdateAll(first, firstFP)

	srv := NewServerWithVariants(newTestStore(t), vm, nil, nil)
	h := srv.Handler()

	paths := []string{
		"/api/services",
		"/api/services/" + testSvc,
		"/api/services/" + testSvc + "/baselines",
		"/api/services/" + testSvc + "/topology-example?fingerprint=" + firstFP,
	}
	for _, p := range paths {
		if w := get(t, h, p); w.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d body %s", p, w.Code, w.Body.String())
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 1; i < 300; i++ {
			tr := testTrace(fmt.Sprintf("t-%d", i), fmt.Sprintf("op-%d", i%40))
			vm.UpdateAll(tr, fp.Compute(tr))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 60; i++ {
			for _, p := range paths {
				get(t, h, p)
			}
		}
	}()
	wg.Wait()
}

func do(t *testing.T, h http.Handler, method, path, origin, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestServer_CORS(t *testing.T) {
	cases := []struct {
		name        string
		env         string
		origin      string
		wantOrigin  string
		wantCredent bool
	}{
		{"default allows compose ui", "", "http://localhost:14000", "http://localhost:14000", true},
		{"default allows vite dev", "", "http://127.0.0.1:3000", "http://127.0.0.1:3000", true},
		{"default rejects other origins", "", "http://evil.example", "", false},
		{"explicit origin", "https://ui.example", "https://ui.example", "https://ui.example", true},
		{"explicit list", "https://a.example, https://b.example", "https://b.example", "https://b.example", true},
		{"explicit list rejects others", "https://a.example", "http://localhost:14000", "", false},
		{"wildcard never sends credentials", "*", "http://evil.example", "*", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CORS_ALLOWED_ORIGIN", tc.env)
			srv := NewServerWithVariants(newTestStore(t), analysis.NewVariantManagerWithOptions(analysis.VariantOptions{}), nil, nil)
			w := do(t, srv.Handler(), http.MethodGet, "/health", tc.origin, "")
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != tc.wantOrigin {
				t.Errorf("Allow-Origin = %q, want %q", got, tc.wantOrigin)
			}
			if got := w.Header().Get("Access-Control-Allow-Credentials") == "true"; got != tc.wantCredent {
				t.Errorf("Allow-Credentials = %v, want %v", got, tc.wantCredent)
			}
		})
	}
}

func TestServer_IngestBodyLimits(t *testing.T) {
	srv := NewServerWithVariants(newTestStore(t), analysis.NewVariantManagerWithOptions(analysis.VariantOptions{}), nil, func(*models.Span) error { return nil })
	h := srv.Handler()

	big := `{"TraceID":"t","SpanID":"s","OperationName":"` + strings.Repeat("x", 1<<20) + `"}`
	if w := do(t, h, http.MethodPost, "/api/spans", "", big); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("single span over 1MB: status %d, want 413", w.Code)
	}
	bigBatch := `[{"TraceID":"t","SpanID":"s","OperationName":"` + strings.Repeat("x", 10<<20) + `"}]`
	if w := do(t, h, http.MethodPost, "/api/spans/batch", "", bigBatch); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("batch over 10MB: status %d, want 413", w.Code)
	}
}

// Ingest runs inline so every accepted span is in the assembler when the
// response is written, and nothing outlives server shutdown.
func TestServer_IngestIsSynchronous(t *testing.T) {
	var got atomic.Int64
	srv := NewServerWithVariants(newTestStore(t), analysis.NewVariantManagerWithOptions(analysis.VariantOptions{}), nil, func(*models.Span) error {
		time.Sleep(time.Millisecond)
		got.Add(1)
		return nil
	})
	h := srv.Handler()

	if w := do(t, h, http.MethodPost, "/api/spans", "", `{"TraceID":"t","SpanID":"s0"}`); w.Code != http.StatusAccepted {
		t.Fatalf("single: status %d", w.Code)
	}
	if got.Load() != 1 {
		t.Fatalf("single span not ingested before response: %d", got.Load())
	}

	batch := `[{"TraceID":"t","SpanID":"s1"},{"TraceID":"t","SpanID":"s2"},{"TraceID":"","SpanID":"bad"}]`
	w := do(t, h, http.MethodPost, "/api/spans/batch", "", batch)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"accepted":2`) || !strings.Contains(w.Body.String(), `"rejected":1`) {
		t.Fatalf("batch: status %d body %s", w.Code, w.Body.String())
	}
	if got.Load() != 3 {
		t.Fatalf("batch spans not ingested before response: %d", got.Load())
	}
}

func TestServer_UnknownVariantOrCombinationRejected(t *testing.T) {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	h := NewServerWithVariants(newTestStore(t), vm, nil, nil).Handler()

	if w := do(t, h, http.MethodPost, "/api/variants/combination", "", `{"combination":"bogus:7"}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown combination: status %d, want 400", w.Code)
	}
	if w := do(t, h, http.MethodPost, "/api/variants/active", "", `{"variant":"bogus"}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown variant: status %d, want 400", w.Code)
	}
	if vm.GetActiveCombination() != "cumulative:50" {
		t.Errorf("active combination changed to %s", vm.GetActiveCombination())
	}
	if w := do(t, h, http.MethodPost, "/api/variants/combination", "", `{"combination":"ewma:500"}`); w.Code != http.StatusOK {
		t.Errorf("valid combination: status %d", w.Code)
	}
	if w := do(t, h, http.MethodPost, "/api/variants/active", "", `{"variant":"sliding_window"}`); w.Code != http.StatusOK {
		t.Errorf("valid variant: status %d", w.Code)
	}
}

// Every combination records its own findings; the API shows the active one.
func TestServer_ListsFilteredByActiveCombination(t *testing.T) {
	store := newTestStore(t)
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	vm.UpdateAll(testTrace("seed", "op"), "fp")
	for _, variant := range []string{"cumulative:50", "ewma:500", "sliding_window:50"} {
		store.StoreDeviation(&models.Deviation{TraceID: "t1", ServiceID: testSvc, Fingerprint: "fp", Score: 0.5, DiffSummary: variant}, variant)
		store.StoreAnomaly(&models.Anomaly{TraceID: "t1", ServiceID: testSvc, Fingerprint: "fp", Score: 9}, variant)
	}
	h := NewServerWithVariants(store, vm, nil, nil).Handler()

	for _, p := range []string{
		"/api/services/" + testSvc + "/deviations",
		"/api/deviations?service_id=" + testSvc,
		"/api/services/" + testSvc + "/anomalies",
		"/api/anomalies?service_id=" + testSvc,
	} {
		w := get(t, h, p)
		if n := strings.Count(w.Body.String(), `"trace_id"`); n != 1 {
			t.Errorf("GET %s: %d rows, want 1 (active combination only): %s", p, n, w.Body.String())
		}
	}
	for _, p := range []string{"/api/services", "/api/services/" + testSvc} {
		body := get(t, h, p).Body.String()
		if !strings.Contains(body, `"deviation_count":1`) || !strings.Contains(body, `"anomaly_count":1`) {
			t.Errorf("GET %s: counts not filtered by active combination: %s", p, body)
		}
	}
}

// The /api/X?service_id= routes answer exactly like /api/services/:id/X.
func TestServer_QueryRoutesMatchPathRoutes(t *testing.T) {
	store := newTestStore(t)
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	vm.UpdateAll(testTrace("seed", "op"), "fp")
	store.StoreDeviation(&models.Deviation{TraceID: "t1", ServiceID: testSvc, Fingerprint: "fp", Score: 0.5, DiffSummary: "d"}, "cumulative:50")
	store.StoreAnomaly(&models.Anomaly{TraceID: "t2", ServiceID: testSvc, Fingerprint: "fp", Score: 9}, "cumulative:50")
	h := NewServerWithVariants(store, vm, []string{"service_identity"}, nil).Handler()

	for _, kind := range []string{"topologies", "deviations", "anomalies"} {
		byPath := get(t, h, "/api/services/"+testSvc+"/"+kind)
		byQuery := get(t, h, "/api/"+kind+"?service_id="+testSvc)
		if byPath.Code != http.StatusOK || byQuery.Code != http.StatusOK {
			t.Fatalf("%s: status path=%d query=%d", kind, byPath.Code, byQuery.Code)
		}
		if byPath.Body.String() != byQuery.Body.String() {
			t.Errorf("%s: path and query bodies differ:\n%s\n%s", kind, byPath.Body.String(), byQuery.Body.String())
		}
		if !strings.Contains(byPath.Body.String(), `"fp"`) {
			t.Errorf("%s: expected fingerprint in body: %s", kind, byPath.Body.String())
		}
		if w := get(t, h, "/api/"+kind); w.Code != http.StatusBadRequest {
			t.Errorf("%s without service_id: status %d, want 400", kind, w.Code)
		}
	}
	if w := get(t, h, "/api/topologies?service_id=missing"); w.Code != http.StatusNotFound {
		t.Errorf("unknown service topologies: status %d, want 404", w.Code)
	}
}

// Stored traces and topology examples share one span JSON shape.
func TestServer_TraceAndTopologyExampleSpanShape(t *testing.T) {
	store := newTestStore(t)
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	trace := testTrace("t-shape", "op")
	trace.Fingerprint = "fp"
	trace.Spans[1].Tags = map[string]string{"error": "true"}
	vm.UpdateAll(trace, "fp")
	if err := store.StoreTrace(trace); err != nil {
		t.Fatalf("StoreTrace: %v", err)
	}
	h := NewServerWithVariants(store, vm, nil, nil).Handler()

	for _, p := range []string{"/api/traces/t-shape", "/api/services/" + testSvc + "/topology-example?fingerprint=fp"} {
		w := get(t, h, p)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d", p, w.Code)
		}
		body := w.Body.String()
		for _, want := range []string{`"span_id":"t-shape-c"`, `"parent_span_id":"t-shape-r"`, `"service_name":"op"`, `"operation_name":"op"`, `"duration_us":5000`, `"status":"ERROR"`, `"attributes":{"error":"true"}`, `"trace_id":"t-shape"`, `"total_duration_us":20000`} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s: missing %s in %s", p, want, body)
			}
		}
	}
}

// GET /api/variants/:variant reports the combination that learns from traffic.
func TestServer_VariantInfoReflectsLiveCombination(t *testing.T) {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	vm.UpdateAll(testTrace("t1", "op"), "fp")
	h := NewServerWithVariants(newTestStore(t), vm, nil, nil).Handler()

	w := get(t, h, "/api/variants/cumulative")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"services":1`) || !strings.Contains(w.Body.String(), `"variant":"cumulative"`) {
		t.Fatalf("cumulative: status %d body %s", w.Code, w.Body.String())
	}
	if w := get(t, h, "/api/variants/bogus"); w.Code != http.StatusNotFound {
		t.Errorf("unknown variant: status %d, want 404", w.Code)
	}
}

func TestServer_CombinationsReflectLearnedSubset(t *testing.T) {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{Combinations: []analysis.VariantCombination{"cumulative:50", "ewma:500"}})
	h := NewServerWithVariants(newTestStore(t), vm, nil, nil).Handler()

	body := get(t, h, "/api/variants/combinations").Body.String()
	if n := strings.Count(body, `"combination":`); n != 2 {
		t.Errorf("combinations listed %d, want 2: %s", n, body)
	}
	if !strings.Contains(body, `"ewma:500"`) || strings.Contains(body, `"ewma:100"`) {
		t.Errorf("combinations list does not match the learned set: %s", body)
	}

	w := do(t, h, http.MethodPost, "/api/variants/combination", "", `{"combination":"ewma:100"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "LEARN_COMBINATIONS") {
		t.Errorf("switch to non-learned: status %d body %s", w.Code, w.Body.String())
	}
	if vm.GetActiveCombination() != "cumulative:50" {
		t.Errorf("active changed to %s", vm.GetActiveCombination())
	}
}

// GET /api/variants/:name accepts a bare variant or a full <variant>:<threshold>
// combination, and always reports the combination it resolved to.
func TestServer_VariantInfoAcceptsVariantOrCombination(t *testing.T) {
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	vm.UpdateAll(testTrace("t1", "op"), "fp")
	h := NewServerWithVariants(newTestStore(t), vm, nil, nil).Handler()

	// A bare variant resolves to its default combination, as documented.
	w := get(t, h, "/api/variants/cumulative")
	if w.Code != http.StatusOK {
		t.Fatalf("bare variant: status %d body %s", w.Code, w.Body.String())
	}
	var bare map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &bare); err != nil {
		t.Fatalf("decode bare: %v", err)
	}
	if got := bare["combination"]; got != "cumulative:50" {
		t.Errorf("bare variant combination = %v, want cumulative:50", got)
	}

	// The combination name itself must resolve, and to itself.
	w = get(t, h, "/api/variants/ewma:500")
	if w.Code != http.StatusOK {
		t.Fatalf("combination name: status %d body %s", w.Code, w.Body.String())
	}
	var byCombo map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &byCombo); err != nil {
		t.Fatalf("decode combo: %v", err)
	}
	if got := byCombo["combination"]; got != "ewma:500" {
		t.Errorf("combination name resolved to %v, want ewma:500", got)
	}
	if got := byCombo["variant"]; got != "ewma" {
		t.Errorf("combination name variant = %v, want ewma", got)
	}
	if got := byCombo["services"]; got != float64(1) {
		t.Errorf("combination name services = %v, want 1 (all combinations learn)", got)
	}

	// A non-default threshold of the same variant resolves to that threshold.
	if w := get(t, h, "/api/variants/cumulative:5000"); w.Code != http.StatusOK {
		t.Errorf("non-default threshold: status %d body %s", w.Code, w.Body.String())
	}

	// An unknown variant and a malformed threshold stay 404, with distinct errors.
	if w := get(t, h, "/api/variants/bogus"); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), "Unknown variant") {
		t.Errorf("unknown variant: status %d body %s", w.Code, w.Body.String())
	}
	if w := get(t, h, "/api/variants/cumulative:abc"); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), "Invalid combination") {
		t.Errorf("bad threshold: status %d body %s", w.Code, w.Body.String())
	}
}

// A service ID can contain a slash, because the operation name it is grouped by
// can be a real HTTP target such as "GET /". Gin decodes %2F into a path
// separator, so such IDs must still resolve on the /api/services/:serviceId
// routes instead of redirecting to a truncated path.
func TestServer_ServiceIDContainingSlash(t *testing.T) {
	const slashID = testSvc + "::::dev::::GET /"

	store := newTestStore(t)
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	tr := &models.Trace{
		TraceID:   "t1",
		ServiceID: slashID,
		StartTime: time.Now(),
		RootSpan:  &models.Span{TraceID: "t1", SpanID: "t1-r", ServiceName: slashID, OperationName: "GET /"},
		Spans:     []*models.Span{{TraceID: "t1", SpanID: "t1-r", ServiceName: slashID, OperationName: "GET /"}},
	}
	if err := store.StoreTrace(tr); err != nil {
		t.Fatalf("store: %v", err)
	}
	vm.UpdateAll(tr, "fp")

	h := NewServerWithVariants(store, vm, nil, nil).Handler()
	enc := url.QueryEscape(slashID)

	for _, suffix := range []string{"", "/topologies", "/deviations", "/anomalies", "/baselines"} {
		w := get(t, h, "/api/services/"+enc+suffix)
		if w.Code != http.StatusOK {
			t.Errorf("GET /api/services/:id%s with a slash in the ID: status %d, want 200 (body %q)",
				suffix, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}

	// The list endpoint must still return the ID unescaped, so the UI can encode it.
	list := get(t, h, "/api/services")
	if !strings.Contains(list.Body.String(), slashID) {
		t.Errorf("service list does not contain the slash ID %q: %s", slashID, list.Body.String())
	}
}
