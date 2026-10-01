package analysis

import (
	"math"
	"sort"

	"github.com/trace-analyzer/internal/models"
)

// AnomalyScorer detects performance anomalies in traces
type AnomalyScorer struct {
	baseline      *BaselineComputer
	fingerprinter *Fingerprinter
	threshold     float64 // Z-score threshold (typically 3.5)
	baselinePct   float64 // Baseline percentile (e.g. 0.5, 0.75, 0.9)
}

// NewAnomalyScorer creates a new anomaly scorer
func NewAnomalyScorer(baseline *BaselineComputer, threshold float64, baselinePct float64) *AnomalyScorer {
	if baselinePct <= 0 || baselinePct >= 1 {
		baselinePct = 0.75
	}
	return &AnomalyScorer{
		baseline:      baseline,
		fingerprinter: defaultFingerprinter,
		threshold:     threshold,
		baselinePct:   baselinePct,
	}
}

// Score evaluates a trace for performance anomalies using per-path baselines
func (s *AnomalyScorer) Score(trace *models.Trace, fingerprint string) *models.Anomaly {
	serviceID := trace.ServiceID

	minSamples := s.baseline.GetMinSamples()

	// same_time_yesterday scores against yesterday's same hour +-2h when that
	// window saw the path often enough, else against the current baseline.
	if s.baseline.GetVariant() == VariantSameTimeYesterday {
		if pb := s.baseline.YesterdayPathBaseline(serviceID, fingerprint, trace.StartTime); pb != nil && pb.Count >= int64(minSamples) {
			return s.scoreWithPathBaseline(trace, fingerprint, pb)
		}
	}

	// Per-topology baseline gate (path-specific)
	var traceScore float64
	scored := false
	s.baseline.withService(serviceID, func(b *ServiceBaseline) {
		if pb := b.PathBaselines[fingerprint]; pb != nil && pb.Count >= int64(minSamples) {
			traceScore = s.computePathZScore(float64(trace.TotalDurationUs), pb)
			scored = true
		}
	})
	if !scored {
		return nil
	}
	return s.anomaly(trace, fingerprint, traceScore)
}

func (s *AnomalyScorer) scoreWithPathBaseline(trace *models.Trace, fingerprint string, pb *PathBaseline) *models.Anomaly {
	return s.anomaly(trace, fingerprint, s.computePathZScore(float64(trace.TotalDurationUs), pb))
}

func (s *AnomalyScorer) anomaly(trace *models.Trace, fingerprint string, traceScore float64) *models.Anomaly {
	if traceScore < s.threshold {
		return nil
	}

	var slowBranches []string
	s.baseline.withService(trace.ServiceID, func(b *ServiceBaseline) {
		slowBranches = s.identifySlowBranches(trace, b)
	})

	return &models.Anomaly{
		TraceID:      trace.TraceID,
		ServiceID:    trace.ServiceID,
		Fingerprint:  fingerprint,
		Score:        traceScore,
		SlowBranches: slowBranches,
		Timestamp:    trace.StartTime,
	}
}

// computePathZScore calculates z-score using path-specific baseline
func (s *AnomalyScorer) computePathZScore(value float64, pb *PathBaseline) float64 {
	pcts := pb.Percentiles(s.baselinePct, 0.25)
	pRef, p25 := pcts[0], pcts[1]
	iqr := pRef - p25
	madEstimate := iqr / 1.35

	if madEstimate == 0 {
		if value == pRef {
			return 0
		}
		// Use min/max range as fallback
		rangeVal := float64(pb.MaxDuration - pb.MinDuration)
		if rangeVal == 0 {
			return 0
		}
		return (value - pRef) / (rangeVal / 4) // Approximate
	}

	modifiedZ := 0.6745 * (value - pRef) / madEstimate
	return math.Abs(modifiedZ)
}

// identifySlowBranches finds branches that contributed most to slowness
func (s *AnomalyScorer) identifySlowBranches(trace *models.Trace, baseline *ServiceBaseline) []string {
	branches := s.fingerprinter.ExtractBranches(trace)

	type branchScore struct {
		key            string
		zScore         float64
		excessDuration int64
	}

	var scores []branchScore

	for _, branch := range branches {
		branchBaseline, exists := baseline.BranchBaselines[branch.Key()]
		if !exists || branchBaseline.Count < 10 {
			continue
		}

		pcts := branchBaseline.Percentiles(s.baselinePct, 0.25, 0.5)
		zScore := branchZScore(float64(branch.DurationUs), pcts[0], pcts[1])
		excess := branch.DurationUs - int64(pcts[2])
		if excess < 0 {
			excess = 0
		}

		if zScore > 2.0 {
			scores = append(scores, branchScore{
				key:            branch.Key(),
				zScore:         zScore,
				excessDuration: excess,
			})
		}
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].excessDuration > scores[j].excessDuration
	})

	var slowBranches []string
	for i, score := range scores {
		if i >= 5 {
			break
		}
		slowBranches = append(slowBranches, score.key)
	}

	return slowBranches
}

// branchZScore calculates z-score for a branch duration from its reference and 25th percentiles
func branchZScore(value, pRef, p25 float64) float64 {
	iqr := pRef - p25
	madEstimate := iqr / 1.35

	if madEstimate == 0 {
		if value == pRef {
			return 0
		}
		return 10.0
	}

	modifiedZ := 0.6745 * (value - pRef) / madEstimate
	return math.Abs(modifiedZ)
}
