package main

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/models"
)

const (
	benchServices = 2
	benchPaths    = 10
	benchVariants = 8
	benchWarmup   = 20000
)

var benchBase = time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)

// benchTrace mirrors the analysis package benchmark: a ternary tree of n spans
// whose second child's service encodes the path.
func benchTrace(rng *rand.Rand, id string, service, path, n int, at time.Time) *models.Trace {
	total := int64(50000 + path*5000 + rng.Intn(5000))
	root := &models.Span{TraceID: id, SpanID: id + "-0", ServiceName: fmt.Sprintf("svc-%d", service), OperationName: "GET /", StartTime: at.UnixMicro(), Duration: total}
	spans := []*models.Span{root}
	for i := 1; i < n; i++ {
		name := fmt.Sprintf("dep-%d", i%6)
		if i == 1 {
			name = fmt.Sprintf("path-%d", path)
		}
		spans = append(spans, &models.Span{
			TraceID: id, SpanID: fmt.Sprintf("%s-%d", id, i), ParentID: spans[(i-1)/3].SpanID,
			ServiceName: name, OperationName: fmt.Sprintf("op-%d", i%4), StartTime: at.UnixMicro() + int64(i), Duration: int64(1000 + rng.Intn(4000)),
		})
	}
	return &models.Trace{
		TraceID: id, Spans: spans, RootSpan: root, ServiceID: fmt.Sprintf("svc-%d:GET /", service),
		StartTime: at, TotalDurationUs: total,
	}
}

type benchItem struct {
	trace       *models.Trace
	fingerprint string
}

type warmedBench struct {
	vm     *analysis.VariantManager
	config *models.Config
	pool   []benchItem
}

// warmedBenches caches the warm-up per span count: the benchmark body is
// re-entered for each b.N probe and -count run, and warm-up dominates runtime.
var warmedBenches = map[int]*warmedBench{}

func warmed(spans int) *warmedBench {
	if w, ok := warmedBenches[spans]; ok {
		return w
	}
	config := models.DefaultConfig()
	window, _ := time.ParseDuration(config.SlidingWindowDuration)
	w := &warmedBench{vm: analysis.NewVariantManagerWithOptions(analysis.VariantOptions{SlidingWindow: window}), config: config}

	rng := rand.New(rand.NewSource(int64(spans)))
	fp := analysis.NewFingerprinter()
	for v := 0; v < benchVariants; v++ {
		for s := 0; s < benchServices; s++ {
			for p := 0; p < benchPaths; p++ {
				at := benchBase.Add(time.Duration(len(w.pool)) * 17 * time.Minute)
				tr := benchTrace(rng, fmt.Sprintf("b-%d", len(w.pool)), s, p, spans, at)
				w.pool = append(w.pool, benchItem{tr, fp.Compute(tr)})
			}
		}
	}
	for i := 0; i < benchWarmup; i++ {
		it := w.pool[i%len(w.pool)]
		w.vm.UpdateAll(it.trace, it.fingerprint)
	}
	warmedBenches[spans] = w
	return w
}

func BenchmarkAnalyzeTrace(b *testing.B) {
	for _, spans := range []int{5, 40} {
		b.Run(fmt.Sprintf("spans=%d", spans), func(b *testing.B) {
			w := warmed(spans)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				it := w.pool[i%len(w.pool)]
				analyzeTrace(w.vm, it.trace, it.fingerprint, w.config)
			}
		})
	}
}
