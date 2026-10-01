package main

import (
	"strings"
	"sync/atomic"

	"github.com/rs/zerolog/log"
	"github.com/trace-analyzer/internal/analysis"
	"github.com/trace-analyzer/internal/models"
	"github.com/trace-analyzer/internal/storage"
)

// pipeline processes each assembled trace: fingerprint, rollup, store, analyze.
type pipeline struct {
	fingerprinter *analysis.Fingerprinter
	variants      *analysis.VariantManager
	store         *storage.SQLiteStorage
	rollups       *storage.CompactionService
	cold          *coldWriter
	config        *models.Config
	stored        atomic.Uint64
}

func (p *pipeline) handle(trace *models.Trace) error {
	log.Debug().Str("trace_id", trace.TraceID).Int("spans", len(trace.Spans)).Msg("Trace handler invoked")
	fingerprint := p.fingerprinter.Compute(trace)
	log.Debug().Str("trace_id", trace.TraceID).Str("fingerprint", fingerprint).Msg("Fingerprint computed")
	trace.Fingerprint = fingerprint

	p.rollups.AddToRollup(trace, fingerprint)

	if err := p.store.StoreTrace(trace); err != nil {
		log.Error().Err(err).Str("trace_id", trace.TraceID).Msg("Failed to store trace")
	} else {
		count := p.stored.Add(1)
		if count <= 5 {
			log.Info().Str("trace_id", trace.TraceID).Uint64("stored_traces", count).Msg("Stored trace")
		} else if count%500 == 0 {
			log.Info().Uint64("stored_traces", count).Msg("Stored traces")
		}
	}

	if trace.HasError {
		p.recordInteresting(trace, "error", 0, "Trace contains error spans", string(p.variants.GetActiveCombination()))
	}

	activeCombination := string(p.variants.GetActiveCombination())

	for _, finding := range analyzeTrace(p.variants, trace, fingerprint, p.config) {
		variantName := finding.variant
		if deviation := finding.deviation; deviation != nil {
			log.Debug().
				Str("trace_id", trace.TraceID).
				Str("variant", variantName).
				Float64("score", deviation.Score).
				Msg("Deviation detected")

			if err := p.store.StoreDeviation(deviation, variantName); err != nil {
				log.Error().Err(err).Str("variant", variantName).Msg("Failed to store deviation")
			}
			// Every combination records its own finding, but the trace is kept once.
			if variantName == activeCombination {
				p.recordInteresting(trace, "deviation", deviation.Score, deviation.DiffSummary, variantName)
			}
		}

		if anomaly := finding.anomaly; anomaly != nil {
			log.Debug().
				Str("trace_id", trace.TraceID).
				Str("variant", variantName).
				Float64("score", anomaly.Score).
				Msg("Anomaly detected")

			if err := p.store.StoreAnomaly(anomaly, variantName); err != nil {
				log.Error().Err(err).Str("variant", variantName).Msg("Failed to store anomaly")
			}
			if variantName == activeCombination {
				summary := ""
				if len(anomaly.SlowBranches) > 0 {
					summary = "Slow branches: " + strings.Join(anomaly.SlowBranches, ", ")
				}
				p.recordInteresting(trace, "anomaly", anomaly.Score, summary, variantName)
			}
		}
	}

	return nil
}

// recordInteresting keeps the trace in the hot tier and queues it for the cold tier.
func (p *pipeline) recordInteresting(trace *models.Trace, reason string, score float64, summary, variant string) {
	if err := p.store.StoreInterestingTrace(trace, reason, score, summary, variant); err != nil {
		log.Warn().Err(err).Str("trace_id", trace.TraceID).Msg("Failed to store interesting " + reason + " trace")
	}
	p.cold.enqueue(&models.InterestingTrace{
		TraceID:     trace.TraceID,
		ServiceID:   trace.ServiceID,
		Fingerprint: trace.Fingerprint,
		Reason:      reason,
		Score:       score,
		Summary:     summary,
		Timestamp:   trace.StartTime,
		Spans:       trace.Spans,
	})
}
