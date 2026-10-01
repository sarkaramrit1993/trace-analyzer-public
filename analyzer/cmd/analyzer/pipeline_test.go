package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/models"
	"github.com/trace-analyzer/internal/storage"
)

type recordingColdStore struct {
	mu      sync.Mutex
	reasons []string
}

func (r *recordingColdStore) WriteInterestingTraces(traces []*models.InterestingTrace) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range traces {
		r.reasons = append(r.reasons, t.Reason)
	}
	return nil
}

func newTestPipeline(t *testing.T, cold *coldWriter) *pipeline {
	t.Helper()
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "pipeline.db"), 15*time.Minute)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return &pipeline{
		fingerprinter: analysis.NewFingerprinter(),
		variants:      analysis.NewVariantManagerWithOptions(analysis.VariantOptions{}),
		store:         store,
		rollups:       storage.NewCompactionService(store, nil, 15*time.Minute, time.Hour, 15*time.Minute),
		cold:          cold,
		config:        models.DefaultConfig(),
	}
}

func TestPipeline_ErrorTraceStoredAsInteresting(t *testing.T) {
	p := newTestPipeline(t, nil)
	trace := pathTrace("err-1", "cart")
	trace.HasError = true

	if err := p.handle(trace); err != nil {
		t.Fatalf("handle: %v", err)
	}

	got, err := p.store.GetInterestingTraces("error", 10)
	if err != nil {
		t.Fatalf("GetInterestingTraces: %v", err)
	}
	if len(got) != 1 || got[0].TraceID != "err-1" || got[0].Reason != "error" {
		t.Fatalf("expected err-1 stored with reason error, got %+v", got)
	}
}

func TestPipeline_NewPathRecordsDeviationOnActiveCombination(t *testing.T) {
	p := newTestPipeline(t, nil)
	for i := 0; i < 100; i++ {
		ops := []string{"cart"}
		if i%5 >= 3 {
			ops = append(ops, "payment")
		}
		if err := p.handle(pathTrace(fmt.Sprintf("est-%d", i), ops...)); err != nil {
			t.Fatalf("handle: %v", err)
		}
	}

	trace := pathTrace("new-1", "cart", "fraud-check")
	if err := p.handle(trace); err != nil {
		t.Fatalf("handle: %v", err)
	}

	active := string(p.variants.GetActiveCombination())
	deviations, err := p.store.GetDeviations(testService, active, 100, 0)
	if err != nil {
		t.Fatalf("GetDeviations: %v", err)
	}
	var newPaths int
	for _, d := range deviations {
		if d.TraceID == "new-1" && strings.HasPrefix(d.DiffSummary, "[new_path]") {
			newPaths++
			if d.Fingerprint != trace.Fingerprint {
				t.Errorf("deviation fingerprint %s, want %s", d.Fingerprint, trace.Fingerprint)
			}
		}
	}
	if newPaths != 1 {
		t.Fatalf("expected 1 new_path deviation for new-1 on %s, got %d", active, newPaths)
	}

	interesting, err := p.store.GetInterestingTraces("deviation", 10)
	if err != nil {
		t.Fatalf("GetInterestingTraces: %v", err)
	}
	if len(interesting) != 1 || interesting[0].TraceID != "new-1" {
		t.Fatalf("expected new-1 kept once as an interesting deviation, got %+v", interesting)
	}
}

func TestPipeline_NilColdWriterIsSkipped(t *testing.T) {
	p := newTestPipeline(t, nil)
	trace := pathTrace("err-nil", "cart")
	trace.HasError = true
	if err := p.handle(trace); err != nil {
		t.Fatalf("handle: %v", err)
	}
}

func TestPipeline_InterestingTraceQueuedForCold(t *testing.T) {
	cold := &recordingColdStore{}
	writer := newColdWriter(cold, 10)
	p := newTestPipeline(t, writer)
	trace := pathTrace("err-cold", "cart")
	trace.HasError = true
	if err := p.handle(trace); err != nil {
		t.Fatalf("handle: %v", err)
	}
	writer.close()
	if len(cold.reasons) != 1 || cold.reasons[0] != "error" {
		t.Fatalf("expected one error record in the cold tier, got %v", cold.reasons)
	}
}
