package analysis

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/trace-analyzer/internal/models"
)

// BaselineVariant represents different baseline computation strategies
type BaselineVariant string

const (
	VariantCumulative        BaselineVariant = "cumulative"          // Default: cumulative counts + reservoir sampling
	VariantEWMA              BaselineVariant = "ewma"                // Exponential Weighted Moving Average
	VariantSlidingWindow     BaselineVariant = "sliding_window"      // Fixed time window (e.g., 1 hour)
	VariantExponentialDecay  BaselineVariant = "exponential_decay"   // Time-weighted exponential decay
	VariantSameTimeYesterday BaselineVariant = "same_time_yesterday" // Compare to same time yesterday (±2h), skip weekends
)

// VariantCombination represents a variant + threshold combination (e.g., "cumulative:50")
type VariantCombination string

// VariantManager manages multiple baseline computers, one per variant+threshold combination
type VariantManager struct {
	combinations      map[VariantCombination]*BaselineComputer // All variant:threshold combinations
	order             []VariantCombination                     // Learned combinations in configured order
	activeCombination VariantCombination
	hourly            *HourlyHistory // shared by same_time_yesterday combinations; nil if none learned
	mu                sync.RWMutex
}

// GetVariantMinSamples returns the minimum samples threshold for a specific variant
// Different variants need different thresholds based on their characteristics:
// - Cumulative: 50 (stable baseline, standard threshold)
// - EWMA: 500 (needs more data for meaningful exponential weighting)
// - Exponential Decay: 5000 (needs substantial data for decay calculations to be meaningful)
func GetVariantMinSamples(variant BaselineVariant) int {
	switch variant {
	case VariantCumulative:
		return 50 // Stable baseline needs sufficient data
	case VariantEWMA:
		return 500 // Needs more data for meaningful exponential weighting
	case VariantExponentialDecay:
		return 5000 // Needs substantial data for decay calculations to be meaningful
	case VariantSameTimeYesterday:
		return 50 // Needs enough data to compare yesterday vs today
	default:
		return 50 // Default fallback
	}
}

// Available thresholds for A/B testing (4 thresholds: 50, 100, 500, 5000)
var AvailableThresholds = []int{50, 100, 500, 5000}

// VariantOptions configures a VariantManager.
type VariantOptions struct {
	// SlidingWindow is the window for sliding_window combinations; zero means 24h.
	SlidingWindow time.Duration
	// Combinations lists the combinations to learn; empty means all 20.
	Combinations []VariantCombination
	// Active is the starting active combination. When empty or not learned,
	// cumulative:50 is used if learned, otherwise the first learned combination.
	Active VariantCombination
	// HourlyPathSampleCap bounds same_time_yesterday samples per hour, service
	// and path; zero means DefaultHourlyPathSampleCap.
	HourlyPathSampleCap int
}

// NewVariantManagerWithOptions creates a variant manager from options.
// Unknown combinations in o.Combinations are skipped.
func NewVariantManagerWithOptions(o VariantOptions) *VariantManager {
	window := o.SlidingWindow
	if window == 0 {
		window = 24 * time.Hour
	}
	learn := o.Combinations
	if len(learn) == 0 {
		learn = AllCombinations()
	}

	vm := &VariantManager{combinations: make(map[VariantCombination]*BaselineComputer)}
	for _, key := range learn {
		variant, threshold, err := ParseCombination(string(key))
		if err != nil {
			continue
		}
		key = VariantCombination(string(variant) + ":" + strconv.Itoa(threshold))
		if _, dup := vm.combinations[key]; dup {
			continue
		}

		bc := NewBaselineComputer(threshold)
		if variant == VariantSameTimeYesterday {
			if vm.hourly == nil {
				vm.hourly = NewHourlyHistory(o.HourlyPathSampleCap)
			}
			bc.useSharedHourly(vm.hourly)
		}
		bc.SetVariant(variant)
		if variant == VariantEWMA {
			bc.SetBaselineAlpha(0.1)
		} else if variant == VariantSlidingWindow {
			bc.SetSlidingWindowDuration(window)
		} else if variant == VariantExponentialDecay {
			bc.SetExponentialDecayHalfLife(24 * time.Hour)
		}

		vm.combinations[key] = bc
		vm.order = append(vm.order, key)
	}

	switch {
	case vm.combinations[o.Active] != nil:
		vm.activeCombination = o.Active
	case vm.combinations[defaultActiveCombination] != nil:
		vm.activeCombination = defaultActiveCombination
	case len(vm.order) > 0:
		vm.activeCombination = vm.order[0]
	}
	return vm
}

// defaultFingerprinter is stateless after construction, so the scorers and
// UpdateAll share one instead of building one per combination per trace.
var defaultFingerprinter = NewFingerprinter()

const defaultActiveCombination VariantCombination = "cumulative:50"

var allVariants = []BaselineVariant{VariantCumulative, VariantEWMA, VariantSlidingWindow, VariantExponentialDecay, VariantSameTimeYesterday}

// AllCombinations returns every variant:threshold combination (5 x 4 = 20).
func AllCombinations() []VariantCombination {
	result := make([]VariantCombination, 0, len(allVariants)*len(AvailableThresholds))
	for _, variant := range allVariants {
		for _, threshold := range AvailableThresholds {
			result = append(result, VariantCombination(string(variant)+":"+strconv.Itoa(threshold)))
		}
	}
	return result
}

// ParseCombination splits "variant:threshold" and checks both parts against
// the known variants and AvailableThresholds.
func ParseCombination(s string) (BaselineVariant, int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("combination %q is not variant:threshold", s)
	}
	variant := BaselineVariant(parts[0])
	if !validVariant(variant) {
		return "", 0, fmt.Errorf("combination %q: unknown variant %q", s, parts[0])
	}
	threshold, err := strconv.Atoi(parts[1])
	if err != nil || !validThreshold(threshold) {
		return "", 0, fmt.Errorf("combination %q: threshold must be one of %v", s, AvailableThresholds)
	}
	return variant, threshold, nil
}

// IsValidVariant reports whether v is one of the known baseline variants.
func IsValidVariant(v BaselineVariant) bool { return validVariant(v) }

func validVariant(v BaselineVariant) bool {
	for _, known := range allVariants {
		if v == known {
			return true
		}
	}
	return false
}

func validThreshold(n int) bool {
	for _, t := range AvailableThresholds {
		if n == t {
			return true
		}
	}
	return false
}

// ParseLearnCombinations reads a LEARN_COMBINATIONS value. "" or "all" returns
// nil (learn all 20). "active" returns only the given active combination.
// Anything else is a comma-separated list of combinations; duplicates are
// dropped and the first invalid entry is an error.
func ParseLearnCombinations(s string, active VariantCombination) ([]VariantCombination, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "all":
		return nil, nil
	case "active":
		s = string(active)
	}
	var result []VariantCombination
	seen := map[VariantCombination]bool{}
	for _, part := range strings.Split(s, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		variant, threshold, err := ParseCombination(part)
		if err != nil {
			return nil, err
		}
		key := VariantCombination(string(variant) + ":" + strconv.Itoa(threshold))
		if !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no combinations in %q", s)
	}
	return result, nil
}

// DefaultCombination is the combination a bare variant name refers to.
func DefaultCombination(variant BaselineVariant) VariantCombination {
	return VariantCombination(string(variant) + ":" + strconv.Itoa(GetVariantMinSamples(variant)))
}

// SetActiveCombination switches the active variant+threshold combination.
// It returns false and changes nothing for an unknown combination.
func (vm *VariantManager) SetActiveCombination(combination VariantCombination) bool {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if _, exists := vm.combinations[combination]; !exists {
		return false
	}
	vm.activeCombination = combination
	return true
}

// GetActiveVariant returns the currently active variant (legacy)
func (vm *VariantManager) GetActiveVariant() BaselineVariant {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	parts := strings.Split(string(vm.activeCombination), ":")
	if len(parts) > 0 {
		return BaselineVariant(parts[0])
	}
	return VariantCumulative
}

// GetActiveCombination returns the currently active combination
func (vm *VariantManager) GetActiveCombination() VariantCombination {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.activeCombination
}

// GetActiveBaseline returns the baseline computer for the active combination
func (vm *VariantManager) GetActiveBaseline() *BaselineComputer {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.combinations[vm.activeCombination]
}

// GetCombination returns the baseline computer for a specific variant+threshold combination
func (vm *VariantManager) GetCombination(combination VariantCombination) *BaselineComputer {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.combinations[combination]
}

// GetAllCombinations returns the learned combinations in configured order
func (vm *VariantManager) GetAllCombinations() []VariantCombination {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return append([]VariantCombination(nil), vm.order...)
}

// UpdateAll teaches the trace to every combination so each builds its own baseline.
func (vm *VariantManager) UpdateAll(trace *models.Trace, fingerprint string) {
	vm.mu.RLock()
	combinations := make([]*BaselineComputer, 0, len(vm.combinations))
	for _, bc := range vm.combinations {
		combinations = append(combinations, bc)
	}
	vm.mu.RUnlock()

	branches := defaultFingerprinter.ExtractBranches(trace)
	for _, bc := range combinations {
		bc.update(trace, fingerprint, branches)
	}
	if vm.hourly != nil {
		vm.hourly.Add(trace.ServiceID, fingerprint, trace.TotalDurationUs, trace.StartTime)
	}
}

// ExportHourlyHistory copies the shared same_time_yesterday buckets (only the
// ones changed since their last export when onlyDirty) and marks them clean.
func (vm *VariantManager) ExportHourlyHistory(onlyDirty bool) []ExportedHourlyBucket {
	if vm.hourly == nil {
		return nil
	}
	return vm.hourly.Export(onlyDirty)
}

// ImportHourlyHistory loads persisted buckets, dropping ones no lookup can reach.
func (vm *VariantManager) ImportHourlyHistory(buckets []ExportedHourlyBucket) {
	if vm.hourly != nil {
		vm.hourly.Import(buckets)
	}
}

// RetainedHourKeys lists the hour keys still reachable by a lookup, sorted.
func (vm *VariantManager) RetainedHourKeys() []string {
	if vm.hourly == nil {
		return nil
	}
	return vm.hourly.RetainedHourKeys()
}

// HourlyStats returns the shared history's bucket and sample counts.
func (vm *VariantManager) HourlyStats() (buckets, samples int) {
	if vm.hourly == nil {
		return 0, 0
	}
	return vm.hourly.stats()
}

// GetAllVariants returns all available variants
func (vm *VariantManager) GetAllVariants() []BaselineVariant {
	return append([]BaselineVariant(nil), allVariants...)
}

// GetVariantInfo returns metadata about a variant
func GetVariantInfo(variant BaselineVariant) map[string]interface{} {
	info := map[string]interface{}{
		"name":        string(variant),
		"description": "",
		"parameters":  map[string]interface{}{},
		"min_samples": GetVariantMinSamples(variant),
	}

	switch variant {
	case VariantCumulative:
		info["description"] = "Cumulative counts with reservoir sampling. Stable, accumulates all history."
		info["parameters"] = map[string]interface{}{
			"method":   "cumulative_counts",
			"sampling": "reservoir_sampling_1000_samples",
		}
	case VariantEWMA:
		info["description"] = "Exponential Weighted Moving Average. Adapts quickly to recent changes while retaining history."
		info["parameters"] = map[string]interface{}{
			"alpha":   0.1,
			"meaning": "Recent data gets 10% weight per update",
		}
	case VariantSlidingWindow:
		// Get actual window duration from a variant instance (if available)
		windowDuration := "24h" // Default
		info["description"] = "Fixed time window. Only considers recent data within the window, adapts to changes."
		info["parameters"] = map[string]interface{}{
			"window_duration": windowDuration,
			"method":          "time_based_filtering",
			"note":            "Window duration configurable via SLIDING_WINDOW_DURATION env var (default: 24h)",
		}
	case VariantExponentialDecay:
		info["description"] = "Time-weighted exponential decay. Older data gradually loses influence."
		info["parameters"] = map[string]interface{}{
			"half_life": "24h",
			"method":    "exponential_time_decay",
		}
	case VariantSameTimeYesterday:
		info["description"] = "Compares current traces to same time yesterday (±2 hours). Skips weekends (uses Friday instead)."
		info["parameters"] = map[string]interface{}{
			"window":       "4 hours (±2h around same time)",
			"comparison":   "yesterday (or Friday if weekend)",
			"timezone":     "UTC",
			"weekend_skip": "yes (uses Friday)",
		}
	}

	return info
}

// GetCombinationInfo returns metadata about a variant+threshold combination
func GetCombinationInfo(combination VariantCombination) map[string]interface{} {
	parts := strings.Split(string(combination), ":")
	if len(parts) != 2 {
		return nil
	}

	variant := BaselineVariant(parts[0])
	threshold, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil
	}

	info := GetVariantInfo(variant)
	info["combination"] = string(combination)
	info["min_samples"] = threshold
	info["variant"] = string(variant)
	info["threshold"] = threshold

	return info
}

// GetAllBaselines returns all baseline computers keyed by variant combination name
// This is used for detecting anomalies/deviations across all variants for A/B testing
func (vm *VariantManager) GetAllBaselines() map[string]*BaselineComputer {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	result := make(map[string]*BaselineComputer)
	for combo, computer := range vm.combinations {
		result[string(combo)] = computer
	}
	return result
}

// ImportBaseline imports a persisted baseline into one combination. It returns
// false when the combination is not learned.
func (vm *VariantManager) ImportBaseline(c VariantCombination, data *ExportedBaseline) bool {
	bc := vm.GetCombination(c)
	if bc == nil {
		return false
	}
	bc.ImportBaseline(data)
	return true
}
