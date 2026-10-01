package analysis

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

func TestBranchKey_CachedEqualsFormatted(t *testing.T) {
	tr := sampleTrace("t", "db", 10, samplesBase)
	tr.Spans[1].OperationName = "SELECT a:b->c"
	branches := NewFingerprinter().ExtractBranches(tr)
	if len(branches) != 1 {
		t.Fatalf("got %d branches, want 1", len(branches))
	}
	b := branches[0]
	want := fmt.Sprintf("%s:%s->%s:%s", b.ParentService, b.ParentOperation, b.ChildService, b.ChildOperation)
	if b.key != want {
		t.Errorf("cached key = %q, want %q", b.key, want)
	}
	if b.Key() != want {
		t.Errorf("Key() = %q, want %q", b.Key(), want)
	}
	literal := Branch{ParentService: b.ParentService, ParentOperation: b.ParentOperation, ChildService: b.ChildService, ChildOperation: b.ChildOperation}
	if literal.Key() != want {
		t.Errorf("uncached Key() = %q, want %q", literal.Key(), want)
	}
}

func TestSortedPercentiles_MatchReference(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	pcts := []float64{-1, 0, 0.25, 0.5, 0.75, 0.99, 1, 2}
	for _, n := range []int{0, 1, 2, 7, 100, 1000} {
		samples := make([]int64, n)
		for i := range samples {
			samples[i] = rng.Int63n(5000)
		}
		orig := slices.Clone(samples)
		if got, want := sortedPercentiles(samples, pcts...), refPercentiles(samples, pcts...); !slices.Equal(got, want) {
			t.Errorf("n=%d: got %v, want %v", n, got, want)
		}
		if !slices.Equal(samples, orig) {
			t.Errorf("n=%d: input was modified", n)
		}
	}
}
