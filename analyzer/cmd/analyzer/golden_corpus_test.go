package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/models"
)

// The golden corpus pins every finding analyzeTrace produces across all
// combinations for a fixed, seeded stream of traces. Regenerate with:
//
//	UPDATE_GOLDEN=1 go test ./cmd/analyzer -run GoldenCorpus
//
// Detection reads trace time only: the 10 minute new-path window, the 5 minute
// recent window and same-time-yesterday retention all follow trace StartTime,
// so results do not depend on how fast the run goes. The one wall-clock read
// is the MaxFutureSkew clamp, which never triggers here because the corpus is
// dated in the past.
//
// Excluded from records: DiffSummary text and CanonicalFingerprint (canonical is
// picked by iterating a map, so ties make it order dependent), SlowBranches
// (sorted with an unstable sort), and timestamps. Only the "[type]" prefix of
// DiffSummary is kept.

const goldenCorpusPath = "testdata/golden_corpus.json"

var corpusBase = time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)

type corpusChild struct {
	parent  int
	service string
	op      string
	frac    float64
}

type corpusTopology struct {
	service   int
	weight    float64
	baseUs    int64
	afterHour float64
	children  []corpusChild
}

type corpusService struct {
	name string
	op   string
}

var corpusServices = []corpusService{
	{"gateway", "GET /checkout"},
	{"search", "GET /search"},
	{"billing", "POST /invoice"},
}

var corpusTopologies = []corpusTopology{
	{service: 0, weight: 40, baseUs: 80000, children: []corpusChild{{0, "cart", "load", 0.3}, {0, "pricing", "quote", 0.4}}},
	{service: 0, weight: 15, baseUs: 120000, children: []corpusChild{{0, "cart", "load", 0.3}, {0, "pricing", "quote", 0.3}, {2, "tax", "calc", 0.2}}},
	{service: 0, weight: 6, baseUs: 95000, children: []corpusChild{{0, "cart", "load", 0.3}, {0, "inventory", "reserve", 0.5}}},
	{service: 0, weight: 0.08, baseUs: 200000, children: []corpusChild{{0, "cart", "load", 0.2}, {0, "fraud", "screen", 0.6}}},
	{service: 0, weight: 1.5, baseUs: 150000, afterHour: 30, children: []corpusChild{{0, "cart", "load", 0.2}, {0, "pricing", "quote", 0.3}, {0, "promo", "apply", 0.3}}},

	{service: 1, weight: 25, baseUs: 30000, children: []corpusChild{{0, "index", "query", 0.7}}},
	{service: 1, weight: 10, baseUs: 45000, children: []corpusChild{{0, "index", "query", 0.5}, {0, "ranker", "score", 0.3}}},
	{service: 1, weight: 3, baseUs: 60000, children: []corpusChild{{0, "index", "query", 0.4}, {0, "ranker", "score", 0.3}, {2, "features", "fetch", 0.2}}},
	{service: 1, weight: 1, baseUs: 50000, afterHour: 36, children: []corpusChild{{0, "index", "query", 0.4}, {0, "spell", "correct", 0.3}}},

	{service: 2, weight: 12, baseUs: 200000, children: []corpusChild{{0, "ledger", "write", 0.5}, {1, "db", "insert", 0.3}}},
	{service: 2, weight: 4, baseUs: 260000, children: []corpusChild{{0, "ledger", "write", 0.4}, {1, "db", "insert", 0.2}, {0, "mailer", "send", 0.3}}},
	{service: 2, weight: 0.05, baseUs: 400000, children: []corpusChild{{0, "ledger", "write", 0.4}, {0, "audit", "record", 0.4}}},
}

type corpusRecord struct {
	Seq         int     `json:"seq"`
	TraceID     string  `json:"trace_id"`
	Combination string  `json:"combination"`
	Kind        string  `json:"kind"`
	Type        string  `json:"type"`
	Score       float64 `json:"score"`
}

func pickTopology(rng *rand.Rand, hour float64) int {
	var total float64
	for _, tp := range corpusTopologies {
		if hour >= tp.afterHour {
			total += tp.weight
		}
	}
	r := rng.Float64() * total
	last := 0
	for i, tp := range corpusTopologies {
		if hour < tp.afterHour {
			continue
		}
		last = i
		if r < tp.weight {
			return i
		}
		r -= tp.weight
	}
	return last
}

func buildCorpusTrace(rng *rand.Rand, seq int, at time.Time, tp corpusTopology) *models.Trace {
	id := fmt.Sprintf("corpus-%05d", seq)
	svc := corpusServices[tp.service]

	jitter := 1 + 0.08*rng.NormFloat64()
	if jitter < 0.5 {
		jitter = 0.5
	}
	if rng.Float64() < 0.006 {
		jitter *= 6
	}
	total := int64(float64(tp.baseUs) * jitter)
	start := at.UnixMicro()

	root := &models.Span{TraceID: id, SpanID: id + "-0", ServiceName: svc.name, OperationName: svc.op, StartTime: start, Duration: total, Tags: map[string]string{}}
	spans := []*models.Span{root}
	hasError := false
	for i, c := range tp.children {
		parent := spans[c.parent]
		tags := map[string]string{"http.status_code": "200"}
		if rng.Float64() < 0.02 {
			tags["http.status_code"] = "500"
			hasError = true
		}
		dur := int64(float64(total) * c.frac * (1 + 0.05*rng.NormFloat64()))
		if dur < 1 {
			dur = 1
		}
		spans = append(spans, &models.Span{
			TraceID: id, SpanID: fmt.Sprintf("%s-%d", id, i+1), ParentID: parent.SpanID,
			ServiceName: c.service, OperationName: c.op, StartTime: parent.StartTime + 100, Duration: dur, Tags: tags,
		})
	}

	return &models.Trace{
		TraceID:         id,
		Spans:           spans,
		RootSpan:        root,
		ServiceID:       svc.name + ":" + svc.op,
		StartTime:       at,
		EndTime:         at.Add(time.Duration(total) * time.Microsecond),
		TotalDurationUs: total,
		HasError:        hasError,
	}
}

func generateCorpus() []*models.Trace {
	const count = 3000
	rng := rand.New(rand.NewSource(20260914))
	step := 48 * time.Hour / count
	traces := make([]*models.Trace, 0, count)
	for i := 0; i < count; i++ {
		at := corpusBase.Add(time.Duration(i)*step + time.Duration(rng.Int63n(int64(step))))
		hour := at.Sub(corpusBase).Hours()
		traces = append(traces, buildCorpusTrace(rng, i, at, corpusTopologies[pickTopology(rng, hour)]))
	}
	return traces
}

func deviationType(summary string) string {
	if strings.HasPrefix(summary, "[") {
		if end := strings.Index(summary, "]"); end > 0 {
			return summary[1:end]
		}
	}
	return "unknown"
}

func round4(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

func runGoldenCorpus(t *testing.T) []corpusRecord {
	t.Helper()
	config := models.DefaultConfig()
	window, err := time.ParseDuration(config.SlidingWindowDuration)
	if err != nil {
		t.Fatalf("default sliding window: %v", err)
	}
	vm := analysis.NewVariantManagerWithOptions(analysis.VariantOptions{SlidingWindow: window})
	fp := analysis.NewFingerprinter()

	var records []corpusRecord
	for seq, trace := range generateCorpus() {
		fingerprint := fp.Compute(trace)
		trace.Fingerprint = fingerprint
		for _, f := range analyzeTrace(vm, trace, fingerprint, config) {
			if f.deviation != nil {
				records = append(records, corpusRecord{seq, trace.TraceID, f.variant, "deviation", deviationType(f.deviation.DiffSummary), round4(f.deviation.Score)})
			}
			if f.anomaly != nil {
				records = append(records, corpusRecord{seq, trace.TraceID, f.variant, "anomaly", "anomaly", round4(f.anomaly.Score)})
			}
		}
	}

	sort.Slice(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.Seq != b.Seq {
			return a.Seq < b.Seq
		}
		if a.Combination != b.Combination {
			return a.Combination < b.Combination
		}
		return a.Kind < b.Kind
	})
	return records
}

func encodeCorpusRecords(records []corpusRecord) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("[\n")
	for i, r := range records {
		line, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		if i < len(records)-1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString("]\n")
	return buf.Bytes(), nil
}

func TestAnalyzeTrace_GoldenCorpus(t *testing.T) {
	records := runGoldenCorpus(t)

	got, err := encodeCorpusRecords(records)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenCorpusPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(goldenCorpusPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %d records (%d bytes) to %s", len(records), len(got), goldenCorpusPath)
		return
	}

	want, err := os.ReadFile(goldenCorpusPath)
	if err != nil {
		t.Fatalf("read golden (run with UPDATE_GOLDEN=1 to create): %v", err)
	}
	if bytes.Equal(got, want) {
		return
	}

	var wantRecords []corpusRecord
	if err := json.Unmarshal(want, &wantRecords); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	t.Errorf("golden corpus mismatch: got %d records, want %d", len(records), len(wantRecords))
	shown := 0
	for i := 0; i < len(records) || i < len(wantRecords); i++ {
		var g, w *corpusRecord
		if i < len(records) {
			g = &records[i]
		}
		if i < len(wantRecords) {
			w = &wantRecords[i]
		}
		if g != nil && w != nil && *g == *w {
			continue
		}
		t.Errorf("record %d: got %+v, want %+v", i, g, w)
		if shown++; shown >= 10 {
			break
		}
	}
}
