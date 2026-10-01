package main

import (
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/trace-analyzer/internal/models"
)

type interestingTraceWriter interface {
	WriteInterestingTraces(traces []*models.InterestingTrace) error
}

// coldWriter hands interesting traces to one background worker. Enqueue never
// blocks the trace handler: a full queue or a closed writer drops the record.
type coldWriter struct {
	store  interestingTraceWriter
	ch     chan *models.InterestingTrace
	mu     sync.RWMutex
	closed bool
	wg     sync.WaitGroup
}

func newColdWriter(store interestingTraceWriter, buffer int) *coldWriter {
	w := &coldWriter{store: store, ch: make(chan *models.InterestingTrace, buffer)}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for record := range w.ch {
			if err := w.store.WriteInterestingTraces([]*models.InterestingTrace{record}); err != nil {
				log.Warn().Err(err).Str("trace_id", record.TraceID).Msg("Failed to write to cold tier")
			}
		}
	}()
	return w
}

func (w *coldWriter) enqueue(record *models.InterestingTrace) {
	if w == nil {
		return
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		log.Warn().Str("trace_id", record.TraceID).Msg("Cold tier writer closed, dropping record")
		return
	}
	select {
	case w.ch <- record:
	default:
		log.Warn().Str("trace_id", record.TraceID).Msg("Cold tier queue full, dropping record")
	}
}

// close stops accepting records and waits for the queued ones to be written.
func (w *coldWriter) close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.ch)
	}
	w.mu.Unlock()
	w.wg.Wait()
}
