package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/models"
)

const testService = "checkout"

func pathTrace(id string, childOps ...string) *models.Trace {
	now := time.Now()
	root := &models.Span{TraceID: id, SpanID: id + "-root", ServiceName: "api-gateway", OperationName: "POST /checkout", StartTime: now.UnixMicro(), Duration: 50000}
	spans := []*models.Span{root}
	for i, op := range childOps {
		spans = append(spans, &models.Span{
			TraceID: id, SpanID: fmt.Sprintf("%s-%d", id, i), ParentID: root.SpanID,
			ServiceName: op, OperationName: op, StartTime: now.UnixMicro() + 1000, Duration: 10000,
		})
	}
	return &models.Trace{TraceID: id, ServiceID: testService, StartTime: now, TotalDurationUs: 50000, RootSpan: root, Spans: spans}
}

type harness struct {
	t      *testing.T
	vm     *analysis.VariantManager
	fp     *analysis.Fingerprinter
	config *models.Config
	seq    int
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t, vm: analysis.NewVariantManagerWithOptions(analysis.VariantOptions{}), fp: analysis.NewFingerprinter(), config: models.DefaultConfig()}
}

// send feeds one trace through analyzeTrace and returns its new_path deviations
// across all combinations, failing the test if one combination reports twice.
func (h *harness) send(childOps ...string) (string, []variantFinding) {
	h.t.Helper()
	h.seq++
	trace := pathTrace(fmt.Sprintf("t-%d", h.seq), childOps...)
	fingerprint := h.fp.Compute(trace)
	trace.Fingerprint = fingerprint

	var newPaths []variantFinding
	seen := map[string]bool{}
	for _, f := range analyzeTrace(h.vm, trace, fingerprint, h.config) {
		if f.deviation == nil || !strings.HasPrefix(f.deviation.DiffSummary, "[new_path]") {
			continue
		}
		if seen[f.variant] {
			h.t.Fatalf("trace %d: %s reported new_path twice", h.seq, f.variant)
		}
		seen[f.variant] = true
		newPaths = append(newPaths, f)
	}
	return fingerprint, newPaths
}

// onActive keeps the findings for the active combination.
func (h *harness) onActive(findings []variantFinding) []variantFinding {
	active := string(h.vm.GetActiveCombination())
	var out []variantFinding
	for _, f := range findings {
		if f.variant == active {
			out = append(out, f)
		}
	}
	return out
}

// establish sends 60 traces of path A and 40 of path B, past the cumulative:50 warm-up.
func (h *harness) establish() {
	h.t.Helper()
	for i := 0; i < 100; i++ {
		ops := []string{"cart"}
		if i%5 >= 3 {
			ops = append(ops, "payment")
		}
		if _, np := h.send(ops...); len(np) != 0 {
			h.t.Fatalf("established path flagged as new at trace %d: %s", i, np[0].deviation.DiffSummary)
		}
	}
}

func TestAnalyzeTrace_FlagsNewPathOnEstablishedService(t *testing.T) {
	h := newHarness(t)
	h.establish()

	fpC, all := h.send("cart", "fraud-check")
	newPaths := h.onActive(all)
	if len(newPaths) != 1 {
		t.Fatalf("expected exactly 1 new_path deviation on the active combination for unseen path C, got %d", len(newPaths))
	}
	got := newPaths[0]
	if got.deviation.Fingerprint != fpC {
		t.Errorf("new_path fingerprint %s, want %s", got.deviation.Fingerprint, fpC)
	}
}

func TestAnalyzeTrace_SecondOccurrenceNotNew(t *testing.T) {
	h := newHarness(t)
	h.establish()

	if _, np := h.send("cart", "fraud-check"); len(h.onActive(np)) != 1 {
		t.Fatalf("first C: expected 1 new_path on the active combination, got %d", len(h.onActive(np)))
	}
	if _, np := h.send("cart", "fraud-check"); len(np) != 0 {
		t.Fatalf("second C flagged as new again: %s", np[0].deviation.DiffSummary)
	}
}

func TestAnalyzeTrace_ColdStartDoesNotFlood(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 50; i++ {
		if _, np := h.send("cart", fmt.Sprintf("unique-%d", i)); len(np) != 0 {
			t.Fatalf("warm-up trace %d flagged as new: %s", i, np[0].deviation.DiffSummary)
		}
	}
}

func TestAnalyzeTrace_EstablishedPathsNotNew(t *testing.T) {
	h := newHarness(t)
	h.establish()
	for i := 0; i < 20; i++ {
		if _, np := h.send("cart"); len(np) != 0 {
			t.Fatalf("path A flagged as new: %s", np[0].deviation.DiffSummary)
		}
		if _, np := h.send("cart", "payment"); len(np) != 0 {
			t.Fatalf("path B flagged as new: %s", np[0].deviation.DiffSummary)
		}
	}
}

// After a restart every combination is seeded from the persisted active baseline
// and keeps learning. A new path may be reported once per combination that is past
// its own warm-up, but its second occurrence must not be reported as new anywhere.
func TestAnalyzeTrace_RestartFromPersistedBaselineNoFlood(t *testing.T) {
	before := newHarness(t)
	before.establish()
	exported := before.vm.GetActiveBaseline().ExportBaseline(testService)

	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{})
	for _, c := range vm.GetAllCombinations() {
		vm.ImportBaseline(c, exported)
	}
	fp := analysis.NewFingerprinter()
	config := models.DefaultConfig()

	newPathsByVariant := func(id string) map[string]int {
		trace := pathTrace(id, "cart", "fraud-check")
		fingerprint := fp.Compute(trace)
		counts := map[string]int{}
		for _, f := range analyzeTrace(vm, trace, fingerprint, config) {
			if f.deviation != nil && strings.HasPrefix(f.deviation.DiffSummary, "[new_path]") {
				counts[f.variant]++
			}
		}
		return counts
	}

	first := newPathsByVariant("c-1")
	if first[string(vm.GetActiveCombination())] != 1 {
		t.Fatalf("active combination did not flag C as new after restart: %v", first)
	}
	for variant, n := range first {
		if n != 1 {
			t.Errorf("%s flagged C %d times in one trace", variant, n)
		}
	}
	t.Logf("first C flagged as new on %d combinations", len(first))

	if second := newPathsByVariant("c-2"); len(second) != 0 {
		t.Fatalf("second C flagged as new after restart: %v", second)
	}
}

func TestAnalyzeTrace_NonActiveCombinationsLearn(t *testing.T) {
	h := newHarness(t)
	h.establish()
	for _, combo := range h.vm.GetAllCombinations() {
		if combo == h.vm.GetActiveCombination() {
			continue
		}
		topologies := h.vm.GetCombination(combo).GetTopologies(testService)
		if len(topologies) == 0 {
			t.Errorf("%s learned nothing after 100 traces", combo)
		}
	}
}

func TestAnalyzeTrace_LearnSubsetOnlyReportsEnabled(t *testing.T) {
	learned := []analysis.VariantCombination{"cumulative:50", "ewma:100"}
	h := newHarness(t)
	h.vm = analysis.NewVariantManagerWithOptions(analysis.VariantOptions{Combinations: learned})
	h.establish()

	_, findings := h.send("cart", "fraud-check")
	if len(findings) == 0 {
		t.Fatal("no findings for an unseen path")
	}
	for _, f := range findings {
		if f.variant != "cumulative:50" && f.variant != "ewma:100" {
			t.Errorf("finding from combination %s that is not learned", f.variant)
		}
	}
}
