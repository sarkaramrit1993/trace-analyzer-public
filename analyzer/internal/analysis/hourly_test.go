package analysis

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"
)

var (
	hMon = time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC) // Monday
	hTue = hMon.AddDate(0, 0, 1)
	hWed = hMon.AddDate(0, 0, 2)
	hFri = hMon.AddDate(0, 0, -3)
	hSat = hMon.AddDate(0, 0, 5)
)

func at(day time.Time, hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

// seedBucket writes an hourly bucket directly, for tests that only care about lookups.
func seedBucket(h *HourlyHistory, t time.Time, svc string, total int64, counts map[string]int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b := h.bucketLocked(hourKey(t), svc)
	b.TotalTraces = total
	for fp, n := range counts {
		b.TopologyCounts[fp] = n
	}
}

func hourlyPathOf(h *HourlyHistory, t time.Time, svc, fp string) *hourlyPath {
	h.mu.RLock()
	defer h.mu.RUnlock()
	b := h.buckets[hourKey(t)][svc]
	if b == nil {
		return nil
	}
	return b.Paths[fp]
}

func TestHourly_BucketRecordsPathDurations(t *testing.T) {
	bc := NewBaselineComputer(10)
	bc.SetVariant(VariantSameTimeYesterday)
	for i, d := range []int64{300, 100, 200} {
		bc.Update(sampleTrace(fmt.Sprintf("t%d", i), "db", d, at(hMon, 10, i)), "fp")
	}
	p := hourlyPathOf(bc.hourly, at(hMon, 10, 0), "svc", "fp")
	if p == nil {
		t.Fatal("hourly bucket has no path entry")
	}
	if p.Count != 3 || p.Min != 100 || p.Max != 300 || p.Sum != 600 || len(p.Samples) != 3 {
		t.Errorf("path = %+v, want Count 3 Min 100 Max 300 Sum 600 and 3 samples", *p)
	}
}

func TestHourly_ReservoirCapKeepsExactCount(t *testing.T) {
	h := NewHourlyHistory(200)
	for i := 0; i < 1000; i++ {
		h.Add("svc", "fp", int64(i+1), at(hMon, 10, i%60))
	}
	p := hourlyPathOf(h, at(hMon, 10, 0), "svc", "fp")
	if p.Count != 1000 || p.Min != 1 || p.Max != 1000 {
		t.Errorf("Count/Min/Max = %d/%d/%d, want 1000/1/1000", p.Count, p.Min, p.Max)
	}
	if len(p.Samples) != 200 || cap(p.Samples) > 200 {
		t.Errorf("samples len %d cap %d, want len 200 cap <= 200", len(p.Samples), cap(p.Samples))
	}
	late := 0
	for _, s := range p.Samples {
		if s > 200 {
			late++
		}
	}
	if late == 0 {
		t.Error("reservoir kept only the first 200 values")
	}
}

func jittered(rng *rand.Rand, base int64) int64 {
	return base + rng.Int63n(base/10) - base/20
}

// Monday 10h runs at ~100ms, Tuesday 08h at ~1000ms. At Tuesday 10:30 a
// 1000ms trace is normal against the cumulative baseline but 10x yesterday.
func TestAnomaly_UsesYesterdayPathBaseline(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{})
	rng := rand.New(rand.NewSource(1))
	fp := NewFingerprinter()
	feed := func(day time.Time, hour int, base int64) {
		for i := 0; i < 60; i++ {
			tr := sampleTrace(fmt.Sprintf("%s-%d", day.Weekday(), i), "db", jittered(rng, base), at(day, hour, i%60))
			vm.UpdateAll(tr, fp.Compute(tr))
		}
	}
	feed(hMon, 10, 100_000)
	feed(hTue, 8, 1_000_000)

	slow := sampleTrace("slow", "db", 1_000_000, at(hTue, 10, 30))
	f := fp.Compute(slow)
	if a := NewAnomalyScorer(vm.GetCombination("cumulative:50"), 3.5, 0.75).Score(slow, f); a != nil {
		t.Fatalf("precondition: cumulative flagged the trace (score %.2f)", a.Score)
	}
	a := NewAnomalyScorer(vm.GetCombination("same_time_yesterday:50"), 3.5, 0.75).Score(slow, f)
	if a == nil {
		t.Fatal("same_time_yesterday did not flag a trace 10x slower than yesterday")
	}
}

func TestAnomaly_FallsBackWhenYesterdayThin(t *testing.T) {
	sty := NewBaselineComputer(50)
	sty.SetVariant(VariantSameTimeYesterday)
	cum := NewBaselineComputer(50)
	rng := rand.New(rand.NewSource(2))
	fp := NewFingerprinter()
	add := func(tr string, d int64, when time.Time) {
		trace := sampleTrace(tr, "db", d, when)
		f := fp.Compute(trace)
		sty.Update(trace, f)
		cum.Update(trace, f)
	}
	for i := 0; i < 5; i++ {
		add(fmt.Sprintf("y%d", i), 5_000_000, at(hMon, 10, i))
	}
	for i := 0; i < 60; i++ {
		add(fmt.Sprintf("t%d", i), jittered(rng, 100_000), at(hTue, 9, i%60))
	}
	slow := sampleTrace("slow", "db", 1_000_000, at(hTue, 10, 30))
	f := fp.Compute(slow)
	got := NewAnomalyScorer(sty, 3.5, 0.75).Score(slow, f)
	want := NewAnomalyScorer(cum, 3.5, 0.75).Score(slow, f)
	if want == nil {
		t.Fatal("precondition: cumulative should flag the slow trace")
	}
	if got == nil || got.Score != want.Score {
		t.Fatalf("thin yesterday: got %+v, want cumulative result %+v", got, want)
	}
}

func TestHourly_PathBaselineMergesWindow(t *testing.T) {
	h := NewHourlyHistory(200)
	var d int64
	for hour := 7; hour <= 13; hour++ {
		for i := 0; i < 4; i++ {
			d++
			h.Add("svc", "fp", d*1000, at(hMon, hour, i))
		}
	}
	pb := h.PathBaseline("svc", "fp", at(hTue, 10, 30))
	if pb == nil {
		t.Fatal("no merged baseline")
	}
	// Hours 08..12 hold durations 5..24 (x1000).
	if pb.Count != 20 || len(pb.Samples) != 20 || pb.MinDuration != 5000 || pb.MaxDuration != 24000 {
		t.Errorf("merged = Count %d samples %d min %d max %d, want 20/20/5000/24000",
			pb.Count, len(pb.Samples), pb.MinDuration, pb.MaxDuration)
	}
	if mean := float64(pb.SumDuration) / float64(pb.Count); mean != 14500 {
		t.Errorf("mean = %v, want 14500", mean)
	}
	if h.PathBaseline("svc", "other", at(hTue, 10, 30)) != nil {
		t.Error("unknown path should return nil")
	}
	pb.Samples[0] = -1
	if again := h.PathBaseline("svc", "fp", at(hTue, 10, 30)); again.Samples[0] == -1 {
		t.Error("PathBaseline returned shared samples, want a fresh copy")
	}
}

func TestHourly_FridayFallbackForMonday(t *testing.T) {
	h := NewHourlyHistory(200)
	for i := 0; i < 5; i++ {
		h.Add("svc", "fp", 1000, at(hFri, 10, i))
	}
	for _, now := range []time.Time{at(hMon, 10, 30), at(hMon.AddDate(0, 0, -1), 10, 30)} {
		if pb := h.PathBaseline("svc", "fp", now); pb == nil || pb.Count != 5 {
			t.Errorf("%s: want Friday's 5 traces, got %+v", now.Weekday(), pb)
		}
		if c, total, ok := h.Frequency("svc", "fp", now); !ok || c != 5 || total != 5 {
			t.Errorf("%s: Frequency = %d/%d/%v, want 5/5/true", now.Weekday(), c, total, ok)
		}
	}
}

func TestRetainedHourKeys(t *testing.T) {
	cases := []struct {
		name   string
		now    time.Time
		count  int
		keep   []time.Time
		forget []time.Time
	}{
		{"tuesday", at(hTue, 10, 0), 101,
			[]time.Time{at(hMon, 8, 0), at(hTue, 10, 0), at(hTue.AddDate(0, 0, 3), 12, 0)},
			[]time.Time{at(hMon, 7, 0), at(hTue.AddDate(0, 0, 3), 13, 0)}},
		{"saturday", at(hSat, 10, 0), 67,
			[]time.Time{at(hSat.AddDate(0, 0, -2), 22, 0), at(hSat.AddDate(0, 0, -1), 8, 0), at(hSat, 1, 0), at(hSat.AddDate(0, 0, 1), 22, 0), at(hSat.AddDate(0, 0, 3), 12, 0)},
			[]time.Time{at(hSat, 2, 0), at(hSat, 10, 0), at(hSat.AddDate(0, 0, 1), 21, 0), at(hSat.AddDate(0, 0, 3), 13, 0)}},
		{"wednesday", at(hWed, 10, 0), 90,
			[]time.Time{at(hTue, 8, 0), at(hWed, 10, 0), at(hSat, 1, 0)},
			[]time.Time{at(hTue, 7, 0), at(hSat, 2, 0)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keys := retainedHourKeys(c.now)
			if len(keys) != c.count {
				t.Errorf("len = %d, want %d", len(keys), c.count)
			}
			for _, k := range c.keep {
				if !keys[hourKey(k)] {
					t.Errorf("%s not retained", hourKey(k))
				}
			}
			for _, k := range c.forget {
				if keys[hourKey(k)] {
					t.Errorf("%s retained, want dropped", hourKey(k))
				}
			}
			for _, k := range comparisonHourKeys(c.now) {
				if !keys[k] {
					t.Errorf("lookup key %s for now is not retained", k)
				}
			}
		})
	}
}

// Replaying January data in September: retention must follow trace time.
func TestHourly_RetentionUsesTraceTime(t *testing.T) {
	mon := time.Date(2026, 1, 26, 0, 0, 0, 0, time.UTC)
	h := NewHourlyHistory(200)
	for i := 0; i < 60; i++ {
		h.Add("svc", "fp", 1000, at(mon, 10, i))
	}
	h.Add("svc", "fp", 1000, at(mon.AddDate(0, 0, 1), 10, 30))
	if pb := h.PathBaseline("svc", "fp", at(mon.AddDate(0, 0, 1), 10, 30)); pb == nil || pb.Count != 60 {
		t.Fatalf("replayed yesterday lost: %+v", pb)
	}
	h.Add("svc", "fp", 1000, at(mon.AddDate(0, 0, 4), 10, 0))
	if hourlyPathOf(h, at(mon, 10, 0), "svc", "fp") != nil {
		t.Error("Monday 10h kept on Friday, want it dropped by trace time")
	}
}

func TestVariantManager_SharedHourlyAcrossThresholds(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{})
	fp := NewFingerprinter()
	const n = 37
	for i := 0; i < n; i++ {
		tr := sampleTrace(fmt.Sprintf("t%d", i), "db", 1000, at(hMon, 10, i))
		vm.UpdateAll(tr, fp.Compute(tr))
	}
	if vm.hourly == nil {
		t.Fatal("variant manager has no hourly history")
	}
	tr := sampleTrace("x", "db", 1000, hMon)
	f := fp.Compute(tr)
	for _, th := range AvailableThresholds {
		bc := vm.GetCombination(VariantCombination(fmt.Sprintf("same_time_yesterday:%d", th)))
		if bc.hourly != vm.hourly {
			t.Errorf("threshold %d has its own history", th)
		}
	}
	if p := hourlyPathOf(vm.hourly, at(hMon, 10, 0), "svc", f); p == nil || p.Count != n {
		t.Errorf("path count = %+v, want %d (one Add per trace)", p, n)
	}

	none := NewVariantManagerWithOptions(VariantOptions{Combinations: []VariantCombination{"cumulative:50"}})
	if none.hourly != nil || none.GetCombination("cumulative:50").hourly != nil {
		t.Error("history allocated without a same_time_yesterday combination")
	}
	if b, s := none.HourlyStats(); b != 0 || s != 0 {
		t.Errorf("HourlyStats = %d/%d, want 0/0", b, s)
	}
}

func TestHourly_ConcurrentScoreCheckUpdateAll(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{})
	fp := NewFingerprinter()
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 180; i++ {
		tr := sampleTrace(fmt.Sprintf("w%d", i), fmt.Sprintf("dep-%d", i%3), jittered(rng, 100_000), at(hMon, 10, i%60))
		vm.UpdateAll(tr, fp.Compute(tr))
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			tr := sampleTrace(fmt.Sprintf("l%d", i), fmt.Sprintf("dep-%d", i%5), int64(100_000+i*1000), at(hTue, 9+i/100, i%60))
			vm.UpdateAll(tr, fp.Compute(tr))
		}
	}()
	for g := 0; g < 4; g++ {
		bc := vm.GetCombination(VariantCombination(fmt.Sprintf("same_time_yesterday:%d", AvailableThresholds[g])))
		wg.Add(1)
		go func() {
			defer wg.Done()
			scorer := NewAnomalyScorer(bc, 3.5, 0.75)
			det := NewDeviationDetector(bc, 0.01)
			for i := 0; i < 300; i++ {
				tr := sampleTrace(fmt.Sprintf("r%d", i), fmt.Sprintf("dep-%d", i%4), 900_000, at(hTue, 10, i%60))
				f := fp.Compute(tr)
				scorer.Score(tr, f)
				det.Check(tr, f)
				if yb := bc.GetYesterdayBaseline("svc", tr.StartTime); yb != nil {
					_ = yb.TopologyCounts[f]
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			vm.ExportHourlyHistory(true)
			vm.HourlyStats()
			vm.RetainedHourKeys()
		}
	}()
	wg.Wait()
}

func TestHourly_ExportImportRoundTrip(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{})
	fp := NewFingerprinter()
	for i := 0; i < 30; i++ {
		tr := sampleTrace(fmt.Sprintf("m%d", i), fmt.Sprintf("dep-%d", i%2), int64(1000+i), at(hMon, 9+i%3, i))
		vm.UpdateAll(tr, fp.Compute(tr))
	}
	tr := sampleTrace("tue", "db", 5000, at(hTue, 10, 30))
	vm.UpdateAll(tr, fp.Compute(tr))

	all := vm.ExportHourlyHistory(false)
	if len(all) != 4 {
		t.Fatalf("exported %d buckets, want 4 (Mon 09-11, Tue 10)", len(all))
	}
	if again := vm.ExportHourlyHistory(true); len(again) != 0 {
		t.Errorf("dirty export after a full export returned %d buckets, want 0", len(again))
	}
	vm.UpdateAll(tr, fp.Compute(tr))
	if dirty := vm.ExportHourlyHistory(true); len(dirty) != 1 || dirty[0].Hour != hourKey(at(hTue, 10, 0)) {
		t.Errorf("dirty export = %+v, want only Tue 10", dirty)
	}
	all = vm.ExportHourlyHistory(false)

	stale := ExportedHourlyBucket{Hour: hourKey(hMon.AddDate(0, 0, -20)), ServiceID: "svc", TotalTraces: 9,
		TopologyCounts: map[string]int64{"old": 9}, Paths: map[string]*ExportedPathBaseline{"old": {Count: 9, Samples: []int64{1}}}}
	fresh := NewVariantManagerWithOptions(VariantOptions{})
	fresh.ImportHourlyHistory(append(append([]ExportedHourlyBucket(nil), all...), stale))
	if got := fresh.ExportHourlyHistory(false); !reflect.DeepEqual(got, all) {
		t.Errorf("round trip differs:\n got %+v\nwant %+v", got, all)
	}
	if keys := fresh.RetainedHourKeys(); len(keys) == 0 || keys[0] != hourKey(at(hMon, 8, 0)) {
		t.Errorf("RetainedHourKeys after import starts at %v, want Mon 08", keys)
	}
	dep0 := fp.Compute(sampleTrace("q", "dep-0", 1, hMon))
	if yb := fresh.GetCombination("same_time_yesterday:50").YesterdayPathBaseline("svc", dep0, at(hTue, 10, 30)); yb == nil || yb.Count != 15 {
		t.Errorf("imported YesterdayPathBaseline = %+v, want Count 15", yb)
	}
	if c, total, ok := fresh.GetCombination("same_time_yesterday:50").YesterdayFrequency("svc", "x", at(hTue, 10, 30)); !ok || c != 0 || total != 10 {
		t.Errorf("imported YesterdayFrequency = %d/%d/%v, want 0/10/true", c, total, ok)
	}
}

func TestHourly_RarePathMemoryBound(t *testing.T) {
	h := NewHourlyHistory(DefaultHourlyPathSampleCap)
	for i := 0; i < 5000; i++ {
		for j := 0; j < 3; j++ {
			h.Add("svc", fmt.Sprintf("fp-%d", i), int64(1000+j), at(hMon, 10, j))
		}
	}
	buckets, samples := h.stats()
	h.mu.RLock()
	capacity := 0
	for _, svcs := range h.buckets {
		for _, b := range svcs {
			for _, p := range b.Paths {
				capacity += cap(p.Samples)
			}
		}
	}
	h.mu.RUnlock()
	bytes := capacity * 8
	t.Logf("5000 rare paths: %d buckets, %d samples, %d bytes of sample capacity", buckets, samples, bytes)
	if samples != 15000 {
		t.Errorf("samples = %d, want 15000", samples)
	}
	if bytes >= 10_000_000 {
		t.Errorf("sample capacity = %d bytes, want < 10 MB", bytes)
	}
}
