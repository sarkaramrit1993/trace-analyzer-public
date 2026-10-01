package analysis

import (
	"fmt"
	"testing"

	"github.com/trace-analyzer/internal/models"
)

// Golden fingerprints pin the WL hash output so refactors of fingerprint.go
// (tag key lists, label hashing) can prove they leave existing hashes intact.
const (
	goldenTwoSpanUntagged = "fee1c9f424665252"
	goldenThreeSpanTagged = "e7c4ed7d9cd96bda"
	goldenFiveSpanTree    = "8ab7e704922fcfdd"
	goldenFanOut40        = "d80cab49a42f6284"
)

func goldenSpan(id, parent, service, op string, tags map[string]string) *models.Span {
	return &models.Span{
		TraceID:       "golden",
		SpanID:        id,
		ParentID:      parent,
		ServiceName:   service,
		OperationName: op,
		StartTime:     1789344000000000,
		Duration:      1000,
		Tags:          tags,
	}
}

func fiveSpanTree(order []int) *models.Trace {
	all := []*models.Span{
		goldenSpan("r", "", "gateway", "GET /orders", nil),
		goldenSpan("a", "r", "orders", "list", nil),
		goldenSpan("b", "r", "auth", "verify", nil),
		goldenSpan("c", "a", "db", "select", nil),
		goldenSpan("d", "a", "cache", "get", nil),
	}
	spans := make([]*models.Span, len(order))
	for i, idx := range order {
		spans[i] = all[idx]
	}
	return &models.Trace{TraceID: "golden", Spans: spans}
}

func fanOutTrace(n int) *models.Trace {
	spans := []*models.Span{goldenSpan("root", "", "fanout", "scatter", nil)}
	for i := 1; i < n; i++ {
		spans = append(spans, goldenSpan(fmt.Sprintf("c%02d", i), "root", fmt.Sprintf("worker-%d", i%7), fmt.Sprintf("shard-%d", i%5), nil))
	}
	return &models.Trace{TraceID: "golden", Spans: spans}
}

func TestFingerprinter_Golden(t *testing.T) {
	tagged := func(identity, scope, env string) map[string]string {
		return map[string]string{"service_identity": identity, "scope1": scope, "env": env}
	}

	cases := []struct {
		name  string
		trace *models.Trace
		want  string
	}{
		{"two span untagged", &models.Trace{Spans: []*models.Span{
			goldenSpan("s1", "", "api", "GET /users", nil),
			goldenSpan("s2", "s1", "db", "query", nil),
		}}, goldenTwoSpanUntagged},
		{"three span neutral tags", &models.Trace{Spans: []*models.Span{
			goldenSpan("s1", "", "api", "POST /pay", tagged("payments-api", "tenant-a", "prod")),
			goldenSpan("s2", "s1", "ledger", "write", tagged("ledger-svc", "tenant-a", "prod")),
			goldenSpan("s3", "s1", "notify", "send", tagged("notify-svc", "tenant-a", "prod")),
		}}, goldenThreeSpanTagged},
		{"five span tree order one", fiveSpanTree([]int{0, 1, 2, 3, 4}), goldenFiveSpanTree},
		{"five span tree shuffled siblings", fiveSpanTree([]int{4, 2, 3, 0, 1}), goldenFiveSpanTree},
		{"forty span fan-out", fanOutTrace(40), goldenFanOut40},
	}

	fp := NewFingerprinter()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fp.Compute(tc.trace); got != tc.want {
				t.Errorf("fingerprint = %q, want %q", got, tc.want)
			}
		})
	}
}
