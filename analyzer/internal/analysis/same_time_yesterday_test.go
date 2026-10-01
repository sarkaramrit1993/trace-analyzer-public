package analysis

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/models"
)

func TestSameTimeYesterday_HistoricalStorage(t *testing.T) {
	bc := NewBaselineComputer(10)
	bc.SetVariant(VariantSameTimeYesterday)

	// Create a trace at a specific time
	traceTime := time.Date(2026, 1, 28, 10, 30, 0, 0, time.UTC)
	trace := &models.Trace{
		TraceID:         "test-1",
		ServiceID:       "test-service",
		StartTime:       traceTime,
		TotalDurationUs: 100000,
		Spans: []*models.Span{
			{SpanID: "span1", OperationName: "op1", ServiceName: "test-service"},
		},
	}

	fingerprint := "fp-test-1"
	bc.Update(trace, fingerprint)

	// Check that the hourly bucket was created
	bc.hourly.mu.RLock()
	svcBaseline, exists := bc.hourly.buckets[hourKey(traceTime)]["test-service"]
	var total, count int64
	if exists {
		total, count = svcBaseline.TotalTraces, svcBaseline.TopologyCounts[fingerprint]
	}
	bc.hourly.mu.RUnlock()

	if !exists {
		t.Fatal("hourly bucket should be created for the trace's hour and service")
	}
	if total != 1 {
		t.Errorf("expected 1 trace in hourly bucket, got %d", total)
	}
	if count != 1 {
		t.Errorf("expected fingerprint count 1, got %d", count)
	}
}

func TestSameTimeYesterday_GetYesterdayBaseline_Weekday(t *testing.T) {
	bc := NewBaselineComputer(10)
	bc.SetVariant(VariantSameTimeYesterday)

	// Tuesday, Jan 28, 2026 at 10:30 AM UTC
	today := time.Date(2026, 1, 28, 10, 30, 0, 0, time.UTC)
	yesterday := today.AddDate(0, 0, -1) // Monday, Jan 27

	// Store baseline for yesterday at same hour
	seedBucket(bc.hourly, yesterday, "test-service", 100, map[string]int64{"fp1": 50})

	// Get yesterday's baseline
	result := bc.GetYesterdayBaseline("test-service", today)

	if result == nil {
		t.Fatal("should find yesterday's baseline")
	}

	if result.TotalTraces != 100 {
		t.Errorf("expected 100 traces, got %d", result.TotalTraces)
	}
}

func TestSameTimeYesterday_GetYesterdayBaseline_WeekendSaturday(t *testing.T) {
	bc := NewBaselineComputer(10)
	bc.SetVariant(VariantSameTimeYesterday)

	// Sunday, Jan 25, 2026 at 10:30 AM UTC (yesterday would be Saturday)
	today := time.Date(2026, 1, 25, 10, 30, 0, 0, time.UTC)
	friday := time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC) // Friday, Jan 23

	// Store baseline for Friday (should be used instead of Saturday)
	seedBucket(bc.hourly, friday, "test-service", 200, map[string]int64{"fp1": 100})

	// Get yesterday's baseline (should return Friday's)
	result := bc.GetYesterdayBaseline("test-service", today)

	if result == nil {
		t.Fatal("should find Friday's baseline when yesterday is Saturday")
	}

	if result.TotalTraces != 200 {
		t.Errorf("expected 200 traces from Friday, got %d", result.TotalTraces)
	}
}

func TestSameTimeYesterday_GetYesterdayBaseline_WeekendSunday(t *testing.T) {
	bc := NewBaselineComputer(10)
	bc.SetVariant(VariantSameTimeYesterday)

	// Monday, Jan 26, 2026 at 10:30 AM UTC (yesterday would be Sunday)
	today := time.Date(2026, 1, 26, 10, 30, 0, 0, time.UTC)
	friday := time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC) // Friday, Jan 23

	// Store baseline for Friday (should be used instead of Sunday)
	seedBucket(bc.hourly, friday, "test-service", 300, map[string]int64{"fp1": 150})

	// Get yesterday's baseline (should return Friday's)
	result := bc.GetYesterdayBaseline("test-service", today)

	if result == nil {
		t.Fatal("should find Friday's baseline when yesterday is Sunday")
	}

	if result.TotalTraces != 300 {
		t.Errorf("expected 300 traces from Friday, got %d", result.TotalTraces)
	}
}

func TestSameTimeYesterday_FourHourWindow(t *testing.T) {
	bc := NewBaselineComputer(10)
	bc.SetVariant(VariantSameTimeYesterday)

	// Tuesday, Jan 28, 2026 at 10:30 AM UTC
	today := time.Date(2026, 1, 28, 10, 30, 0, 0, time.UTC)
	yesterday := today.AddDate(0, 0, -1) // Monday, Jan 27

	// Store baselines for different hours around same time (±2 hours)
	hours := []int{8, 9, 10, 11, 12} // 8 AM to 12 PM
	for _, hour := range hours {
		checkTime := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), hour, 0, 0, 0, time.UTC)
		seedBucket(bc.hourly, checkTime, "test-service", int64(100+hour), map[string]int64{"fp1": int64(50 + hour)})
	}

	// Get yesterday's baseline - should find one in the 4-hour window
	result := bc.GetYesterdayBaseline("test-service", today)

	if result == nil {
		t.Fatal("should find baseline in 4-hour window")
	}

	// Should find the one with most traces (12 PM = 112 traces)
	if result.TotalTraces != 112 {
		t.Errorf("expected 112 traces (from 12 PM), got %d", result.TotalTraces)
	}
}

func TestSameTimeYesterday_NoHistoricalBaseline(t *testing.T) {
	bc := NewBaselineComputer(10)
	bc.SetVariant(VariantSameTimeYesterday)

	// Tuesday, Jan 28, 2026 at 10:30 AM UTC
	today := time.Date(2026, 1, 28, 10, 30, 0, 0, time.UTC)

	// No historical data stored
	result := bc.GetYesterdayBaseline("test-service", today)

	if result != nil {
		t.Error("should return nil when no historical baseline exists")
	}
}

func TestSameTimeYesterday_UpdateFrequency(t *testing.T) {
	bc := NewBaselineComputer(10)
	bc.SetVariant(VariantSameTimeYesterday)

	traceTime := time.Date(2026, 1, 28, 10, 30, 0, 0, time.UTC)
	trace := &models.Trace{
		TraceID:         "test-1",
		ServiceID:       "test-service",
		StartTime:       traceTime,
		TotalDurationUs: 100000,
		Spans: []*models.Span{
			{SpanID: "span1", OperationName: "op1", ServiceName: "test-service"},
		},
	}

	fingerprint := "fp-test-1"
	bc.Update(trace, fingerprint)

	// Check current baseline was updated
	currentBaseline := bc.serviceBaseline("test-service")
	if currentBaseline == nil {
		t.Fatal("current baseline should exist")
	}

	if currentBaseline.TopologyCounts[fingerprint] != 1 {
		t.Errorf("current baseline should have fingerprint count 1, got %d", currentBaseline.TopologyCounts[fingerprint])
	}

	// Check the hourly bucket was also updated
	c, total, ok := bc.hourly.Frequency("test-service", fingerprint, traceTime.AddDate(0, 0, 1))
	if !ok || c != 1 || total != 1 {
		t.Errorf("hourly bucket should have fingerprint count 1 of 1, got %d of %d (ok=%v)", c, total, ok)
	}
}

func TestSameTimeYesterday_VariantInitialization(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{})
	combinations := vm.GetAllCombinations()

	// Check that SameTimeYesterday combinations exist
	found := false
	for _, combo := range combinations {
		if variant, _, err := ParseCombination(string(combo)); err == nil && variant == VariantSameTimeYesterday {
			found = true
			break
		}
	}

	if !found {
		t.Error("SameTimeYesterday variant should be initialized in variant manager")
	}

	// Check variant info
	info := GetVariantInfo(VariantSameTimeYesterday)
	if info == nil {
		t.Fatal("variant info should exist")
	}

	if info["name"] != string(VariantSameTimeYesterday) {
		t.Errorf("expected name %s, got %v", VariantSameTimeYesterday, info["name"])
	}
}

func TestSameTimeYesterday_EndToEnd(t *testing.T) {
	bc := NewBaselineComputer(10)
	bc.SetVariant(VariantSameTimeYesterday)
	fingerprinter := NewFingerprinter()

	// Day 1: Store traces for yesterday
	yesterday := time.Date(2026, 1, 27, 10, 30, 0, 0, time.UTC) // Monday
	for i := 0; i < 5; i++ {
		trace := &models.Trace{
			TraceID:         fmt.Sprintf("yesterday-%d", i),
			ServiceID:       "test-service",
			StartTime:       yesterday.Add(time.Duration(i) * time.Minute),
			TotalDurationUs: 100000 + int64(i*1000),
			Spans: []*models.Span{
				{SpanID: fmt.Sprintf("span-%d", i), OperationName: "op1", ServiceName: "test-service"},
			},
		}
		fp := fingerprinter.Compute(trace)
		bc.Update(trace, fp)
	}

	// Day 2: Process trace today and compare to yesterday
	today := time.Date(2026, 1, 28, 10, 30, 0, 0, time.UTC) // Tuesday
	trace := &models.Trace{
		TraceID:         "today-1",
		ServiceID:       "test-service",
		StartTime:       today,
		TotalDurationUs: 200000, // Different duration
		Spans: []*models.Span{
			{SpanID: "span-today", OperationName: "op1", ServiceName: "test-service"},
		},
	}
	fp := fingerprinter.Compute(trace)
	bc.Update(trace, fp)

	// Get yesterday's baseline
	yesterdayBaseline := bc.GetYesterdayBaseline("test-service", today)
	if yesterdayBaseline == nil {
		t.Fatal("should find yesterday's baseline")
	}

	if yesterdayBaseline.TotalTraces < 5 {
		t.Errorf("expected at least 5 traces in yesterday's baseline, got %d", yesterdayBaseline.TotalTraces)
	}
}

func TestSameTimeYesterday_VariantManager(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{})

	// Check all variants are initialized
	variants := vm.GetAllVariants()
	expectedVariants := 5
	if len(variants) != expectedVariants {
		t.Errorf("expected %d variants, got %d", expectedVariants, len(variants))
	}

	// Check SameTimeYesterday is included
	found := false
	for _, v := range variants {
		if v == VariantSameTimeYesterday {
			found = true
			break
		}
	}
	if !found {
		t.Error("SameTimeYesterday variant should be in GetAllVariants")
	}

	// Check combinations exist
	combinations := vm.GetAllCombinations()
	expectedCombinations := 20 // 5 variants × 4 thresholds
	if len(combinations) != expectedCombinations {
		t.Errorf("expected %d combinations, got %d", expectedCombinations, len(combinations))
	}

	// Check SameTimeYesterday combinations exist
	sameTimeCombos := 0
	for _, combo := range combinations {
		if strings.HasPrefix(string(combo), string(VariantSameTimeYesterday)+":") {
			sameTimeCombos++
		}
	}
	if sameTimeCombos != 4 {
		t.Errorf("expected 4 SameTimeYesterday combinations, got %d", sameTimeCombos)
	}
}

func TestSameTimeYesterday_WeekendHandling(t *testing.T) {
	bc := NewBaselineComputer(10)
	bc.SetVariant(VariantSameTimeYesterday)

	// Test Saturday -> Friday
	friday := time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC) // Friday

	seedBucket(bc.hourly, friday, "test-service", 100, nil)

	// Sunday (yesterday would be Saturday, should use Friday)
	sunday := time.Date(2026, 1, 25, 10, 30, 0, 0, time.UTC)
	result := bc.GetYesterdayBaseline("test-service", sunday)
	if result == nil {
		t.Fatal("should find Friday's baseline when yesterday is Saturday")
	}

	// Monday (yesterday would be Sunday, should use Friday)
	monday := time.Date(2026, 1, 26, 10, 30, 0, 0, time.UTC)
	result2 := bc.GetYesterdayBaseline("test-service", monday)
	if result2 == nil {
		t.Fatal("should find Friday's baseline when yesterday is Sunday")
	}
}
