package analysis

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/models"
)

func raceTrace(id string, op string) *models.Trace {
	now := time.Now()
	root := &models.Span{TraceID: id, SpanID: id + "-root", ServiceName: "gw", OperationName: "GET /", StartTime: now.UnixMicro(), Duration: 20000}
	child := &models.Span{TraceID: id, SpanID: id + "-c", ParentID: root.SpanID, ServiceName: op, OperationName: op, StartTime: now.UnixMicro() + 10, Duration: 5000}
	return &models.Trace{TraceID: id, ServiceID: "svc", StartTime: now, TotalDurationUs: 20000, RootSpan: root, Spans: []*models.Span{root, child}}
}

// Combinations seeded from one persisted export must not share maps: each is
// guarded by its own lock, so shared maps race between UpdateAll and readers.
func TestVariantManager_ImportedCombinationsDoNotShareState(t *testing.T) {
	seed := NewBaselineComputer(1)
	fp := NewFingerprinter()
	for i := 0; i < 20; i++ {
		tr := raceTrace(fmt.Sprintf("seed-%d", i), fmt.Sprintf("op-%d", i%3))
		seed.Update(tr, fp.Compute(tr))
	}
	exported := seed.ExportBaseline("svc")

	vm := NewVariantManagerWithOptions(VariantOptions{})
	for _, c := range vm.GetAllCombinations() {
		vm.ImportBaseline(c, exported)
	}
	reader := vm.GetCombination("ewma:100")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			tr := raceTrace(fmt.Sprintf("live-%d", i), fmt.Sprintf("new-op-%d", i))
			vm.UpdateAll(tr, fp.Compute(tr))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = reader.GetTopologies("svc")
		}
	}()
	wg.Wait()

	exported.TopologyCounts["mutated-after-import"] = 1
	for _, combo := range vm.GetAllCombinations() {
		for _, topo := range vm.GetCombination(combo).GetTopologies("svc") {
			if topo.Fingerprint == "mutated-after-import" {
				t.Fatalf("%s shares TopologyCounts with the imported export", combo)
			}
		}
	}
}
