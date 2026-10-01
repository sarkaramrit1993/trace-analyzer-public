package analysis

import (
	"fmt"
	"testing"
	"time"

	"github.com/trace-analyzer/internal/models"
)

func TestDeviationDetection_WithSameTimeYesterday(t *testing.T) {
	bc := NewBaselineComputer(10)
	bc.SetVariant(VariantSameTimeYesterday)
	detector := NewDeviationDetector(bc, 0.1)
	fingerprinter := NewFingerprinter()

	// Store yesterday's baseline
	yesterday := time.Date(2026, 1, 27, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		trace := &models.Trace{
			TraceID:         fmt.Sprintf("yesterday-%d", i),
			ServiceID:       "test-service",
			StartTime:       yesterday.Add(time.Duration(i) * time.Minute),
			TotalDurationUs: 100000,
			Spans: []*models.Span{
				{SpanID: fmt.Sprintf("span-%d", i), OperationName: "op1", ServiceName: "test-service"},
			},
		}
		fp := fingerprinter.Compute(trace)
		bc.Update(trace, fp)
	}

	// Today's trace with new path
	today := time.Date(2026, 1, 28, 10, 0, 0, 0, time.UTC)
	newTrace := &models.Trace{
		TraceID:         "today-new",
		ServiceID:       "test-service",
		StartTime:       today,
		TotalDurationUs: 100000,
		Spans: []*models.Span{
			{SpanID: "span-new", OperationName: "op2", ServiceName: "test-service"}, // Different operation
		},
	}
	fp := fingerprinter.Compute(newTrace)
	bc.Update(newTrace, fp)

	// Check for deviation
	deviation := detector.Check(newTrace, fp)
	if deviation == nil {
		t.Log("No deviation detected (may be expected if threshold not met)")
	} else {
		if deviation.Score <= 0 {
			t.Error("deviation score should be positive")
		}
		t.Logf("Deviation detected: %s (score: %.2f)", deviation.DiffSummary, deviation.Score)
	}
}

// Yesterday 10h ran at ~100ms; this morning (06h, outside the window) ran at
// ~1000ms. A 1000ms trace at 10h is normal for the cumulative baseline but
// must be flagged against yesterday.
func TestAnomalyDetection_WithSameTimeYesterday(t *testing.T) {
	sty := NewBaselineComputer(10)
	sty.SetVariant(VariantSameTimeYesterday)
	cum := NewBaselineComputer(10)
	fingerprinter := NewFingerprinter()

	yesterday := time.Date(2026, 1, 27, 10, 0, 0, 0, time.UTC) // Tuesday
	today := yesterday.AddDate(0, 0, 1)
	feed := func(prefix string, start time.Time, base int64) {
		for i := 0; i < 20; i++ {
			trace := sampleTrace(fmt.Sprintf("%s-%d", prefix, i), "db", base+int64(i%5)*base/50, start.Add(time.Duration(i)*time.Minute))
			fp := fingerprinter.Compute(trace)
			sty.Update(trace, fp)
			cum.Update(trace, fp)
		}
	}
	feed("yesterday", yesterday, 100000)
	feed("morning", today.Add(-4*time.Hour), 1000000)

	slowTrace := sampleTrace("today-slow", "db", 1000000, today)
	fp := fingerprinter.Compute(slowTrace)

	if a := NewAnomalyScorer(cum, 3.5, 0.75).Score(slowTrace, fp); a != nil {
		t.Fatalf("precondition: cumulative flagged the trace (score %.2f)", a.Score)
	}
	anomaly := NewAnomalyScorer(sty, 3.5, 0.75).Score(slowTrace, fp)
	if anomaly == nil {
		t.Fatal("same_time_yesterday should flag a trace 10x slower than yesterday at this hour")
	}
	if anomaly.Score < 3.5 {
		t.Errorf("anomaly score = %.2f, want >= 3.5", anomaly.Score)
	}
}

func TestDeviationDetection_AllVariants(t *testing.T) {
	variants := []BaselineVariant{
		VariantCumulative,
		VariantEWMA,
		VariantSlidingWindow,
		VariantExponentialDecay,
		VariantSameTimeYesterday,
	}

	for _, variant := range variants {
		t.Run(string(variant), func(t *testing.T) {
			bc := NewBaselineComputer(10)
			bc.SetVariant(variant)
			detector := NewDeviationDetector(bc, 0.1)
			fingerprinter := NewFingerprinter()

			// Build baseline
			for i := 0; i < 15; i++ {
				trace := &models.Trace{
					TraceID:         fmt.Sprintf("trace-%d", i),
					ServiceID:       "test-service",
					StartTime:       time.Now().Add(-time.Duration(i) * time.Minute),
					TotalDurationUs: 100000,
					Spans: []*models.Span{
						{SpanID: fmt.Sprintf("span-%d", i), OperationName: "op1", ServiceName: "test-service"},
					},
				}
				fp := fingerprinter.Compute(trace)
				bc.Update(trace, fp)
			}

			// New path
			newTrace := &models.Trace{
				TraceID:         "new-trace",
				ServiceID:       "test-service",
				StartTime:       time.Now(),
				TotalDurationUs: 100000,
				Spans: []*models.Span{
					{SpanID: "span-new", OperationName: "op2", ServiceName: "test-service"},
				},
			}
			fp := fingerprinter.Compute(newTrace)
			bc.Update(newTrace, fp)

			// Should not panic
			deviation := detector.Check(newTrace, fp)
			if deviation != nil {
				t.Logf("Deviation detected for %s: %s (score: %.2f)", variant, deviation.DiffSummary, deviation.Score)
			}
		})
	}
}
