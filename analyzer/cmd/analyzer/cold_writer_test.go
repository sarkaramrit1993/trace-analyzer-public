package main

import (
	"sync"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/models"
)

type blockingColdStore struct {
	release chan struct{}
	mu      sync.Mutex
	written int
}

func (b *blockingColdStore) WriteInterestingTraces(traces []*models.InterestingTrace) error {
	<-b.release
	b.mu.Lock()
	b.written += len(traces)
	b.mu.Unlock()
	return nil
}

func TestColdWriter_SlowStoreDoesNotBlockEnqueue(t *testing.T) {
	store := &blockingColdStore{release: make(chan struct{})}
	w := newColdWriter(store, 2)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			w.enqueue(&models.InterestingTrace{TraceID: "t"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueue blocked on a stalled cold store")
	}
	close(store.release)
	w.close()
	if store.written == 0 || store.written > 3 {
		t.Fatalf("expected the buffered records (1..3) to be written, got %d", store.written)
	}
}

// Traces flushed during shutdown may reach the handler after the writer closed.
func TestColdWriter_EnqueueAfterCloseDoesNotPanic(t *testing.T) {
	store := &blockingColdStore{release: make(chan struct{})}
	close(store.release)
	w := newColdWriter(store, 4)
	w.close()
	w.enqueue(&models.InterestingTrace{TraceID: "late"})
}

func TestColdWriter_NilIsNoop(t *testing.T) {
	var w *coldWriter
	w.enqueue(&models.InterestingTrace{TraceID: "t"})
	w.close()
}
