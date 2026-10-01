package analysis

import (
	"time"

	"github.com/trace-analyzer/internal/models"
)

// Update teaches one combination a trace. Production feeds every combination
// through VariantManager.UpdateAll.
func (bc *BaselineComputer) Update(trace *models.Trace, fingerprint string) {
	bc.update(trace, fingerprint, bc.fingerprinter.ExtractBranches(trace))
}

// serviceBaseline returns the live baseline, whose maps change under bc.mu.
func (bc *BaselineComputer) serviceBaseline(serviceID string) *ServiceBaseline {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.services[serviceID]
}

// GetYesterdayBaseline returns a copy of the busiest bucket among yesterday's
// same hour +-2h (Friday's when yesterday is a weekend day), or nil.
func (bc *BaselineComputer) GetYesterdayBaseline(serviceID string, currentTime time.Time) *ServiceBaseline {
	h := bc.hourlyHistory()
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	b := h.bestBucketLocked(serviceID, currentTime)
	if b == nil {
		return nil
	}
	sb := &ServiceBaseline{
		ServiceID:      serviceID,
		TotalTraces:    b.TotalTraces,
		TopologyCounts: make(map[string]int64, len(b.TopologyCounts)),
		PathBaselines:  make(map[string]*PathBaseline, len(b.Paths)),
	}
	for fp, n := range b.TopologyCounts {
		sb.TopologyCounts[fp] = n
	}
	for fp, p := range b.Paths {
		sb.PathBaselines[fp] = &PathBaseline{Count: p.Count, SumDuration: p.Sum, MinDuration: p.Min, MaxDuration: p.Max, Samples: append([]int64(nil), p.Samples...)}
	}
	return sb
}
