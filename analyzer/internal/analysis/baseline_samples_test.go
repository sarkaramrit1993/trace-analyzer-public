package analysis

import (
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/models"
)

var samplesBase = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

// sampleTrace is a two-span trace whose root and child both take d microseconds.
func sampleTrace(id, child string, d int64, at time.Time) *models.Trace {
	root := &models.Span{TraceID: id, SpanID: id + "-r", ServiceName: "gw", OperationName: "GET /", StartTime: at.UnixMicro(), Duration: d}
	c := &models.Span{TraceID: id, SpanID: id + "-c", ParentID: root.SpanID, ServiceName: child, OperationName: "op", StartTime: at.UnixMicro() + 1, Duration: d}
	return &models.Trace{TraceID: id, ServiceID: "svc", StartTime: at, TotalDurationUs: d, RootSpan: root, Spans: []*models.Span{root, c}}
}

func TestBaseline_LazySampleAllocation(t *testing.T) {
	bc := NewBaselineComputer(1)
	bc.Update(sampleTrace("t", "db", 100, samplesBase), "fp")

	sb := bc.services["svc"]
	if c := cap(sb.PathBaselines["fp"].Samples); c >= 1000 {
		t.Errorf("path samples cap = %d, want < 1000", c)
	}
	for key, bb := range sb.BranchBaselines {
		if c := cap(bb.Samples); c >= 100 {
			t.Errorf("branch %s samples cap = %d, want < 100", key, c)
		}
	}
	if c := cap(sb.DurationSamples); c >= 1000 {
		t.Errorf("service DurationSamples cap = %d, want < 1000", c)
	}
}

// ringRef mirrors the reservoir rule: append until full, then overwrite slot idx%limit.
func ringRef(ref []int64, v int64, limit int, idx int64) []int64 {
	if len(ref) < limit {
		return append(ref, v)
	}
	ref[int(idx%int64(limit))] = v
	return ref
}

func TestBaseline_RingSemanticsUnchanged(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{})
	type refs struct {
		path, branch, service []int64
		pathCount             int64
	}
	want := map[VariantCombination]*refs{}
	for _, c := range vm.GetAllCombinations() {
		want[c] = &refs{}
	}
	const branchKey = "gw:GET /->db:op"

	for i := 0; i < 2500; i++ {
		d := int64(i)
		vm.UpdateAll(sampleTrace(fmt.Sprintf("t%d", i), "db", d, samplesBase.Add(time.Duration(i)*time.Second)), "fp")
		for c, r := range want {
			sb := vm.GetCombination(c).services["svc"]
			pb := sb.PathBaselines["fp"]
			// Exponential decay picks the slot before its periodic Count decay;
			// sliding window decays before incrementing, so its final Count is the slot.
			slot := pb.Count
			if vm.GetCombination(c).variant == VariantExponentialDecay {
				slot = r.pathCount + 1
			}
			r.pathCount = pb.Count
			r.path = ringRef(r.path, d, 1000, slot)
			r.branch = ringRef(r.branch, d, 100, sb.BranchBaselines[branchKey].Count)
			r.service = ringRef(r.service, d, 1000, sb.TotalTraces)
		}
	}

	for c, r := range want {
		sb := vm.GetCombination(c).services["svc"]
		if !slices.Equal(sb.PathBaselines["fp"].Samples, r.path) {
			t.Errorf("%s: path samples differ from reference", c)
		}
		if !slices.Equal(sb.BranchBaselines[branchKey].Samples, r.branch) {
			t.Errorf("%s: branch samples differ from reference", c)
		}
		if !slices.Equal(sb.DurationSamples, r.service) {
			t.Errorf("%s: service samples differ from reference", c)
		}
	}
}

func sampleCapacity(vm *VariantManager) int {
	total := 0
	addService := func(sb *ServiceBaseline) {
		total += cap(sb.DurationSamples)
		for _, pb := range sb.PathBaselines {
			total += cap(pb.Samples)
		}
		for _, bb := range sb.BranchBaselines {
			total += cap(bb.Samples)
		}
	}
	for _, bc := range vm.GetAllBaselines() {
		for _, sb := range bc.services {
			addService(sb)
		}
	}
	if vm.hourly != nil {
		vm.hourly.mu.RLock()
		for _, svcs := range vm.hourly.buckets {
			for _, b := range svcs {
				for _, p := range b.Paths {
					total += cap(p.Samples)
				}
			}
		}
		vm.hourly.mu.RUnlock()
	}
	return total
}

// Each rare path gets its own fingerprint and its own branch, the worst case
// for per-path and per-branch sample slices.
func TestUpdateAll_RarePathMemoryBound(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{})
	for i := 0; i < 2000; i++ {
		vm.UpdateAll(sampleTrace(fmt.Sprintf("t%d", i), fmt.Sprintf("dep-%d", i), int64(1000+i), samplesBase), fmt.Sprintf("fp-%d", i))
	}
	bytes := sampleCapacity(vm) * 8
	t.Logf("sample capacity across %d combinations: %d bytes (%.2f MB)", len(vm.GetAllCombinations()), bytes, float64(bytes)/1e6)
	if bytes >= 6_000_000 {
		t.Errorf("sample capacity = %d bytes, want < 6 MB", bytes)
	}
}

func refPercentiles(samples []int64, pcts ...float64) []float64 {
	out := make([]float64, len(pcts))
	if len(samples) == 0 {
		return out
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	for i, p := range pcts {
		p = max(0, min(1, p))
		out[i] = float64(sorted[int(float64(len(sorted)-1)*p)])
	}
	return out
}

func TestPercentiles_MatchReference(t *testing.T) {
	pcts := []float64{-0.5, 0, 0.01, 0.25, 0.5, 0.75, 0.9, 0.99, 1, 1.5}
	rng := rand.New(rand.NewSource(7))
	bc := NewBaselineComputer(1)
	const branchKey = "gw:GET /->db:op"

	check := func(stage string) {
		t.Helper()
		pb := bc.services["svc"].PathBaselines["fp"]
		bb := bc.services["svc"].BranchBaselines[branchKey]
		if got, want := pb.Percentiles(pcts...), refPercentiles(pb.Samples, pcts...); !slices.Equal(got, want) {
			t.Errorf("%s: path Percentiles = %v, want %v", stage, got, want)
		}
		if got, want := bb.Percentiles(pcts...), refPercentiles(bb.Samples, pcts...); !slices.Equal(got, want) {
			t.Errorf("%s: branch Percentiles = %v, want %v", stage, got, want)
		}
		for _, p := range pcts {
			if got, want := pb.Percentiles(p)[0], refPercentiles(pb.Samples, p)[0]; got != want {
				t.Errorf("%s: path Percentile(%v) = %v, want %v", stage, p, got, want)
			}
			if got, want := bb.Percentiles(p)[0], refPercentiles(bb.Samples, p)[0]; got != want {
				t.Errorf("%s: branch Percentile(%v) = %v, want %v", stage, p, got, want)
			}
		}
	}

	update := func(n int) {
		for i := 0; i < n; i++ {
			bc.Update(sampleTrace("t", "db", rng.Int63n(1_000_000), samplesBase), "fp")
		}
	}

	update(1)
	check("one sample")
	update(37)
	check("partial")
	for round := 0; round < 5; round++ {
		update(rng.Intn(700) + 1)
		check(fmt.Sprintf("round %d", round))
	}

	empty := &PathBaseline{}
	if got := empty.Percentiles(0.5, 0.9); !slices.Equal(got, []float64{0, 0}) {
		t.Errorf("empty Percentiles = %v, want zeros", got)
	}

	pb := bc.services["svc"].PathBaselines["fp"]
	bb := bc.services["svc"].BranchBaselines[branchKey]
	wantPath := refPercentiles(pb.Samples, pcts...)
	wantBranch := refPercentiles(bb.Samples, pcts...)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if got := pb.Percentiles(pcts...); !slices.Equal(got, wantPath) {
					t.Errorf("concurrent path Percentiles = %v, want %v", got, wantPath)
					return
				}
				if got := bb.Percentiles(0.5)[0]; got != refPercentiles(bb.Samples, 0.5)[0] {
					t.Errorf("concurrent branch Percentile = %v", got)
					return
				}
				if got := bb.Percentiles(pcts...); !slices.Equal(got, wantBranch) {
					t.Errorf("concurrent branch Percentiles = %v, want %v", got, wantBranch)
					return
				}
			}
		}()
	}
	wg.Wait()
}
