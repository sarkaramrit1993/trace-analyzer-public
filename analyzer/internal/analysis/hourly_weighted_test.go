package analysis

import (
	"reflect"
	"testing"
)

const (
	busyUs  = 1_000_000 // ~1000ms
	quietUs = 100_000   // ~100ms
)

// fillHour adds n traces at ~d microseconds to Monday's given hour.
func fillHour(h *HourlyHistory, hour, n int, d int64) {
	for i := 0; i < n; i++ {
		h.Add("svc", "fp", d+int64(i%10), at(hMon, hour, i%60))
	}
}

func near(got, want float64) bool { return got >= want && got < want+10 }

func TestHourly_MergedPercentilesWeightHoursByCount(t *testing.T) {
	h := NewHourlyHistory(DefaultHourlyPathSampleCap)
	fillHour(h, 10, 10000, busyUs)
	for _, hour := range []int{8, 9, 11, 12} {
		fillHour(h, hour, 50, quietUs)
	}
	pb := h.PathBaseline("svc", "fp", at(hTue, 10, 30))
	if pb.Count != 10200 || pb.MinDuration != quietUs || pb.MaxDuration != busyUs+9 {
		t.Fatalf("Count/Min/Max = %d/%d/%d, want exact 10200/%d/%d", pb.Count, pb.MinDuration, pb.MaxDuration, quietUs, busyUs+9)
	}
	// 200 quiet traces out of 10200 are under 2%, so P5 and up are busy.
	for _, pct := range []float64{0.05, 0.25, 0.5, 0.99} {
		if got := pb.Percentiles(pct)[0]; !near(got, busyUs) {
			t.Errorf("P%v = %v, want ~%d (busy hour holds 98%% of traces)", pct*100, got, busyUs)
		}
	}
	if got := pb.Percentiles(0.01)[0]; !near(got, quietUs) {
		t.Errorf("P1 = %v, want ~%d", got, quietUs)
	}
}

func TestHourly_TwoHourMergeQuietHourDoesNotDragPercentiles(t *testing.T) {
	h := NewHourlyHistory(DefaultHourlyPathSampleCap)
	fillHour(h, 10, 10000, busyUs)
	fillHour(h, 11, 50, quietUs)
	pb := h.PathBaseline("svc", "fp", at(hTue, 10, 30))
	for _, pct := range []float64{0.1, 0.5} {
		if got := pb.Percentiles(pct)[0]; !near(got, busyUs) {
			t.Errorf("P%v = %v, want ~%d", pct*100, got, busyUs)
		}
	}
}

func TestHourly_SingleHourPercentilesUnchanged(t *testing.T) {
	h := NewHourlyHistory(DefaultHourlyPathSampleCap)
	for i := 0; i < 1000; i++ {
		h.Add("svc", "fp", int64(i*37%1000), at(hMon, 10, i%60))
	}
	pb := h.PathBaseline("svc", "fp", at(hTue, 10, 30))
	pcts := []float64{0, 0.25, 0.5, 0.9, 0.99, 1}
	want := sortedPercentiles(hourlyPathOf(h, at(hMon, 10, 0), "svc", "fp").Samples, pcts...)
	if got := pb.Percentiles(pcts...); !reflect.DeepEqual(got, want) {
		t.Errorf("single hour percentiles = %v, want plain reservoir %v", got, want)
	}
}

func TestHourly_UncappedHoursMatchPlainPercentiles(t *testing.T) {
	h := NewHourlyHistory(DefaultHourlyPathSampleCap)
	fillHour(h, 9, 30, quietUs)
	fillHour(h, 10, 70, busyUs)
	pb := h.PathBaseline("svc", "fp", at(hTue, 10, 30))
	pcts := []float64{0, 0.1, 0.3, 0.31, 0.5, 0.99, 1}
	if got, want := pb.Percentiles(pcts...), sortedPercentiles(pb.Samples, pcts...); !reflect.DeepEqual(got, want) {
		t.Errorf("hours under the cap: %v, want plain %v", got, want)
	}
}

func TestHourly_MergedPercentilesDeterministic(t *testing.T) {
	build := func() []float64 {
		h := NewHourlyHistory(DefaultHourlyPathSampleCap)
		fillHour(h, 10, 5000, busyUs)
		fillHour(h, 12, 300, quietUs)
		return h.PathBaseline("svc", "fp", at(hTue, 10, 30)).Percentiles(0.1, 0.25, 0.5, 0.9, 0.99)
	}
	if a, b := build(), build(); !reflect.DeepEqual(a, b) {
		t.Errorf("same input gave %v then %v", a, b)
	}
}
