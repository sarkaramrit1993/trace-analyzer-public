package analysis

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/models"
)

const (
	benchServices = 2
	benchPaths    = 10
	benchVariants = 8
	benchWarmup   = 20000
)

var benchBase = time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)

// benchTrace builds a ternary tree of n spans whose second child's service
// encodes the path, so each (service, path) pair has its own fingerprint.
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

func benchPool(spans int) []benchItem {
	rng := rand.New(rand.NewSource(int64(spans)))
	fp := NewFingerprinter()
	var pool []benchItem
	for v := 0; v < benchVariants; v++ {
		for s := 0; s < benchServices; s++ {
			for p := 0; p < benchPaths; p++ {
				at := benchBase.Add(time.Duration(len(pool)) * 17 * time.Minute)
				tr := benchTrace(rng, fmt.Sprintf("b-%d", len(pool)), s, p, spans, at)
				pool = append(pool, benchItem{tr, fp.Compute(tr)})
			}
		}
	}
	return pool
}

type warmedBench struct {
	vm   *VariantManager
	pool []benchItem
}

// warmedBenches caches the warm-up per span count: the benchmark body is
// re-entered for each b.N probe and -count run, and warm-up dominates runtime.
var warmedBenches = map[int]*warmedBench{}

func warmed(spans int) *warmedBench {
	if w, ok := warmedBenches[spans]; ok {
		return w
	}
	w := &warmedBench{vm: NewVariantManagerWithOptions(VariantOptions{}), pool: benchPool(spans)}
	for i := 0; i < benchWarmup; i++ {
		it := w.pool[i%len(w.pool)]
		w.vm.UpdateAll(it.trace, it.fingerprint)
	}
	warmedBenches[spans] = w
	return w
}

func BenchmarkUpdateAll(b *testing.B) {
	for _, spans := range []int{5, 40} {
		b.Run(fmt.Sprintf("spans=%d", spans), func(b *testing.B) {
			w := warmed(spans)
			vm, pool := w.vm, w.pool
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				it := pool[i%len(pool)]
				vm.UpdateAll(it.trace, it.fingerprint)
			}
		})
	}
}
