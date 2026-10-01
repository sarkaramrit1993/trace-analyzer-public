package analysis

import (
	"fmt"

	"github.com/trace-analyzer/internal/models"
)

// DeviationType categorizes the kind of deviation
type DeviationType string

const (
	DeviationNewPath        DeviationType = "new_path"        // Never seen before
	DeviationRarePath       DeviationType = "rare_path"       // <1% frequency
	DeviationFrequencyDrop  DeviationType = "frequency_drop"  // Known path suddenly rare
	DeviationFrequencySpike DeviationType = "frequency_spike" // Rare path suddenly common
)

// DeviationDetector identifies traces that deviate from known-good topologies
type DeviationDetector struct {
	baseline      *BaselineComputer
	fingerprinter *Fingerprinter
	rareThreshold float64 // Below this frequency = rare (default 0.01 = 1%)
}

// NewDeviationDetector creates a new deviation detector
func NewDeviationDetector(baseline *BaselineComputer, rareThreshold float64) *DeviationDetector {
	if rareThreshold <= 0 {
		rareThreshold = 0.01 // 1% default
	}
	return &DeviationDetector{
		baseline:      baseline,
		fingerprinter: NewFingerprinter(),
		rareThreshold: rareThreshold,
	}
}

// Check checks if a trace deviates from known-good paths
func (d *DeviationDetector) Check(trace *models.Trace, fingerprint string) *models.Deviation {
	serviceID := trace.ServiceID

	var totalTraces, pathCount int64
	if !d.baseline.withService(serviceID, func(b *ServiceBaseline) {
		totalTraces, pathCount = b.TotalTraces, b.TopologyCounts[fingerprint]
	}) {
		return nil // No baseline yet
	}
	// Use the baseline computer's minSamples threshold (variant-specific)
	minSamples := d.baseline.GetMinSamples()
	if totalTraces < int64(minSamples) {
		return nil // Not enough data yet (variant-specific threshold)
	}

	// Check 1: Is this a completely new path?
	isNew := d.baseline.IsNewPath(serviceID, fingerprint, trace.StartTime)
	if !isNew && pathCount < int64(minSamples) {
		return nil
	}
	// same_time_yesterday takes the frequency from yesterday's busiest
	// comparison hour when there is one.
	var freq float64
	count, total, ok := int64(0), int64(0), false
	if d.baseline.GetVariant() == VariantSameTimeYesterday {
		count, total, ok = d.baseline.YesterdayFrequency(serviceID, fingerprint, trace.StartTime)
	}
	if ok && total > 0 {
		freq = float64(count) / float64(total)
	} else {
		freq = d.baseline.GetPathFrequency(serviceID, fingerprint)
	}

	if isNew && freq == 0 {
		// Brand new path - always flag
		return d.createDeviation(trace, fingerprint, DeviationNewPath, 1.0,
			"New execution path detected (never seen before)")
	}

	// Check 2: Is this a known-good path?
	if d.baseline.IsKnownGoodPath(serviceID, fingerprint) {
		// Known good path - check for frequency shifts
		shifted, oldFreq, newFreq := d.baseline.DetectFrequencyShift(serviceID, fingerprint)
		if shifted {
			if newFreq < oldFreq {
				return d.createDeviation(trace, fingerprint, DeviationFrequencyDrop,
					(oldFreq-newFreq)/oldFreq,
					fmt.Sprintf("Path frequency dropped: %.1f%% → %.1f%%", oldFreq*100, newFreq*100))
			}
		}
		return nil // Known good, no deviation
	}

	// Check 3: Is this a rare path (<1%)?
	if freq < d.rareThreshold {
		// Check if it's spiking
		shifted, oldFreq, newFreq := d.baseline.DetectFrequencyShift(serviceID, fingerprint)
		if shifted && newFreq > oldFreq && newFreq > 0.05 {
			return d.createDeviation(trace, fingerprint, DeviationFrequencySpike,
				newFreq-oldFreq,
				fmt.Sprintf("Rare path spiking: %.1f%% → %.1f%%", oldFreq*100, newFreq*100))
		}

		// Just a rare path
		if freq < 0.001 { // <0.1% - very rare
			var diffSummary string
			d.baseline.withService(serviceID, func(b *ServiceBaseline) {
				diffSummary = d.generateDiffSummary(trace, fingerprint, b)
			})
			return d.createDeviation(trace, fingerprint, DeviationRarePath,
				1.0-freq*100, // Higher score for rarer paths
				diffSummary)
		}
	}

	return nil
}

func (d *DeviationDetector) createDeviation(trace *models.Trace, fingerprint string,
	devType DeviationType, score float64, summary string) *models.Deviation {

	canonical := d.baseline.GetCanonicalFingerprint(trace.ServiceID)

	return &models.Deviation{
		TraceID:              trace.TraceID,
		ServiceID:            trace.ServiceID,
		Fingerprint:          fingerprint,
		CanonicalFingerprint: canonical,
		Score:                score,
		Timestamp:            trace.StartTime,
		DiffSummary:          fmt.Sprintf("[%s] %s", devType, summary),
	}
}

// generateDiffSummary creates a human-readable description of differences
func (d *DeviationDetector) generateDiffSummary(trace *models.Trace, fingerprint string, baseline *ServiceBaseline) string {
	canonical := baseline.CanonicalFingerprint
	if canonical == "" {
		return "No canonical path established yet"
	}

	canonicalTrace, exists := baseline.TopologyExamples[canonical]
	if !exists {
		return "Unable to compare (no canonical example)"
	}

	// Extract and compare branches
	traceBranches := d.fingerprinter.ExtractBranches(trace)
	canonicalBranches := d.fingerprinter.ExtractBranches(canonicalTrace)

	traceBranchSet := make(map[string]bool)
	for _, b := range traceBranches {
		traceBranchSet[b.Key()] = true
	}

	canonicalBranchSet := make(map[string]bool)
	for _, b := range canonicalBranches {
		canonicalBranchSet[b.Key()] = true
	}

	// Find added and removed branches
	var added, removed []string

	for k := range traceBranchSet {
		if !canonicalBranchSet[k] {
			added = append(added, k)
		}
	}

	for k := range canonicalBranchSet {
		if !traceBranchSet[k] {
			removed = append(removed, k)
		}
	}

	summary := ""
	if len(added) > 0 {
		summary += fmt.Sprintf("Added: %v. ", added)
	}
	if len(removed) > 0 {
		summary += fmt.Sprintf("Missing: %v. ", removed)
	}
	if summary == "" {
		summary = "Structural difference in call graph"
	}

	// Check for recursion
	if hasRecursion(trace) {
		summary += " [Contains recursive calls]"
	}

	return summary
}

// hasRecursion detects if a trace contains recursive service calls
// Uses DFS from root - O(s) complexity instead of O(s*d)
func hasRecursion(trace *models.Trace) bool {
	if len(trace.Spans) == 0 {
		return false
	}

	// Build parent->children map and find root
	children := make(map[string][]*models.Span)
	var root *models.Span

	for _, span := range trace.Spans {
		if span.IsRoot() {
			root = span
		} else {
			children[span.ParentID] = append(children[span.ParentID], span)
		}
	}

	if root == nil {
		return false
	}

	// DFS with path tracking (only track current path, not all visited)
	return hasRecursionDFS(root, children, make(map[string]bool))
}

// hasRecursionDFS performs depth-first search tracking service:operation in current path
func hasRecursionDFS(span *models.Span, children map[string][]*models.Span, pathSeen map[string]bool) bool {
	key := span.ServiceName + ":" + span.OperationName

	// Check if this service:operation is already in current path (recursion!)
	if pathSeen[key] {
		return true
	}

	// Add to current path
	pathSeen[key] = true

	// Check all children
	for _, child := range children[span.SpanID] {
		if hasRecursionDFS(child, children, pathSeen) {
			return true
		}
	}

	// Remove from path (backtrack)
	delete(pathSeen, key)

	return false
}
