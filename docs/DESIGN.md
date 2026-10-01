# Design Overview

## Core idea

Service-level metrics tell you *that* a service got slower. Per-path analysis tells you *which execution path* through that service is responsible.

## How it works

```
SSE ingest → Assembler (180s window) → Fingerprinter (WL graph hash)
                                                    ↓
                                VariantManager: 20 parallel baselines
                                                    ↓
                                 DeviationDetector + AnomalyScorer
                                                    ↓
                                     SQLite (hot) → S3-compatible (cold)
```

- **Fingerprinting**: Weisfeiler-Lehman graph hashing (20 iterations). 712 HotROD traces → ~5 fingerprints.
- **20 combinations**: 4 variants × 4 min-sample thresholds. Each combination learns independently from the same traffic, flags against its own history.
- **Variants**: `cumulative` (stable), `ewma` (fast adaptation), `sliding_window` (fixed window), `exponential_decay` (gradual forgetting), `same_time_yesterday` (daily seasonality).
- **Drift detection**: new path, rare path, frequency shift. **Anomaly**: weighted z-score against path latency percentile (default P75).
- **Cold tier**: hourly rollups + interesting traces (errors, deviations, anomalies) to S3-compatible storage (SeaweedFS in compose).

## Key decisions

| Decision | Trade-off |
|----------|-----------|
| All 20 combinations learn by default | ~17 µs CPU/trace, ~5 MB per 2000 paths. Configurable via `LEARN_COMBINATIONS`. |
| Baseline persistence per combination | Restart restores every combination's state. Legacy rows upgrade gracefully. |
| Trace time, not wall clock | Fast ingest/replay no longer marks every path "new" for 10 min. |
| Per-path `LastUpdateTime` in baselines | Decay and new-path windows compare like with like after restart. |

## Known limits (honest)

- No ground-truth evaluation of detection accuracy.
- Fingerprint granularity is coarse on simple apps (HotROD → 5 fingerprints). Real services with input-dependent fan-out produce many more.
- `same_time_yesterday` only compares against hourly history, not full seasonality.
- Only active combination survives restart unless `ACTIVE_COMBINATION` is set.

## Tech stack

Go 1.21+, React 18 + TypeScript, SQLite, SeaweedFS, GitHub Actions CI.
