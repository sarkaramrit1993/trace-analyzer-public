package analysis

import (
	"testing"
	"time"

	"github.com/trace-analyzer/internal/models"
)

func TestBaselineVariants_AllVariantsWork(t *testing.T) {
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

			trace := &models.Trace{
				TraceID:         "test-1",
				ServiceID:       "test-service",
				StartTime:       time.Now(),
				TotalDurationUs: 100000,
				Spans: []*models.Span{
					{SpanID: "span1", OperationName: "op1", ServiceName: "test-service"},
				},
			}

			fingerprinter := NewFingerprinter()
			fp := fingerprinter.Compute(trace)

			// Should not panic
			bc.Update(trace, fp)

			baseline := bc.serviceBaseline("test-service")
			if baseline == nil {
				t.Fatal("baseline should be created")
			}

			if baseline.TotalTraces != 1 {
				t.Errorf("expected 1 trace, got %d", baseline.TotalTraces)
			}
		})
	}
}

func TestVariantManager_SwitchCombinations(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{})

	// Test switching between combinations
	combinations := []VariantCombination{
		"cumulative:50",
		"ewma:500",
		"sliding_window:100",
		"exponential_decay:5000",
		"same_time_yesterday:50",
	}

	for _, combo := range combinations {
		vm.SetActiveCombination(combo)
		active := vm.GetActiveCombination()
		if active != combo {
			t.Errorf("expected active combination %s, got %s", combo, active)
		}

		baseline := vm.GetActiveBaseline()
		if baseline == nil {
			t.Errorf("baseline should exist for combination %s", combo)
		}
	}
}

func TestVariantManager_AllCombinationsUpdate(t *testing.T) {
	vm := NewVariantManagerWithOptions(VariantOptions{})
	fingerprinter := NewFingerprinter()

	trace := &models.Trace{
		TraceID:         "test-update",
		ServiceID:       "test-service",
		StartTime:       time.Now(),
		TotalDurationUs: 100000,
		Spans: []*models.Span{
			{SpanID: "span1", OperationName: "op1", ServiceName: "test-service"},
		},
	}

	fp := fingerprinter.Compute(trace)

	// Update all variants
	vm.UpdateAll(trace, fp)

	// Check that all combinations were updated
	combinations := vm.GetAllCombinations()
	for _, combo := range combinations {
		bc := vm.GetCombination(combo)
		if bc == nil {
			t.Errorf("combination %s should exist", combo)
			continue
		}

		baseline := bc.serviceBaseline("test-service")
		if baseline == nil {
			t.Errorf("baseline should exist for combination %s", combo)
		} else if baseline.TotalTraces != 1 {
			t.Errorf("expected 1 trace for combination %s, got %d", combo, baseline.TotalTraces)
		}
	}
}

func TestGetVariantMinSamples(t *testing.T) {
	tests := []struct {
		variant  BaselineVariant
		expected int
	}{
		{VariantCumulative, 50},
		{VariantEWMA, 500},
		{VariantSlidingWindow, 50},
		{VariantExponentialDecay, 5000},
		{VariantSameTimeYesterday, 50},
	}

	for _, tt := range tests {
		result := GetVariantMinSamples(tt.variant)
		if result != tt.expected {
			t.Errorf("GetVariantMinSamples(%s) = %d, want %d", tt.variant, result, tt.expected)
		}
	}
}

func TestGetVariantInfo(t *testing.T) {
	variants := []BaselineVariant{
		VariantCumulative,
		VariantEWMA,
		VariantSlidingWindow,
		VariantExponentialDecay,
		VariantSameTimeYesterday,
	}

	for _, variant := range variants {
		info := GetVariantInfo(variant)
		if info == nil {
			t.Errorf("GetVariantInfo(%s) returned nil", variant)
			continue
		}

		if info["name"] != string(variant) {
			t.Errorf("expected name %s, got %v", variant, info["name"])
		}

		if info["min_samples"] == nil {
			t.Errorf("min_samples should be set for %s", variant)
		}
	}
}
