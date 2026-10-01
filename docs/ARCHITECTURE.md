# Architecture & Engineering Decisions

## System Overview

```
┌─────────────┐     ┌──────────────┐     ┌────────────────┐     ┌─────────────────┐
│  SSE/HTTP   │────▶│  Assembler   │────▶│  Fingerprinter │────▶│ VariantManager  │
│  Ingestion  │     │  (180s window)│     │  (WL hash)     │     │  (20 combos)    │
└─────────────┘     └──────────────┘     └────────────────┘     └────────┬────────┘
                                                                          │
                    ┌─────────────────────────────────────────────────────┘
                    ▼
        ┌─────────────────────┐   ┌─────────────────────┐
        │ DeviationDetector   │   │ AnomalyScorer       │
        │ (freq shift, new,   │   │ (weighted z-score   │
        │  rare path)         │   │  vs P75)            │
        └──────────┬──────────┘   └──────────┬──────────┘
                   │                         │
                   └─────────────┬───────────┘
                                 ▼
                    ┌────────────────────────┐
                    │ SQLite (hot) + S3      │
                    │ (cold, opt-in profile) │
                    └────────────────────────┘
```

## Key Architectural Decisions

| Decision | Rationale | Trade-off |
|----------|-----------|-----------|
| **Single Go process** | No network hops, no serialization, trivial ops | Can't scale ingestion/analysis independently |
| **Per-path baselines** | Service averages hide path-level regressions | 20× memory/CPU vs single baseline |
| **20 combinations** | Different strategies catch different failure modes | More false positives; needs `LEARN_COMBINATIONS` to tune |
| **Trace time, not wall clock** | Fast ingest/replay doesn't mark everything "new" | Requires monotonic trace timestamps |
| **Baseline persistence per combo** | Restart preserves all 20 states, not just active | ~4 MB per 2000 paths × 20 combos |
| **SQLite + S3, no external DB** | Zero-infra demo; portable | No HA, no concurrent writers |
| **React Flow for topology** | Interactive, good UX for graph viz | Bundle size (~180 KB gzipped) |

## Data Flow Invariants

1. **Trace assembly**: Spans arrive → assembler buffers by `TraceID` → 180s timeout → complete trace emitted
2. **Fingerprinting**: WL hash on call graph → 64-bit ID. Collisions possible but negligible at service scale.
3. **20 parallel baselines**: Every learned combo processes every trace. Deviation/anomaly checks run per-combo.
3. **Active combo drives UI**: Only the active combo's hits become interesting traces and show in lists.
4. **Persistence**: Every 5 min + shutdown, all 20 combos' baselines + hourly history written to SQLite.
5. **Cold tier**: Hourly rollup flush + interesting trace queue → S3-compatible (SeaweedFS in compose).

## Why Not X?

| X | Reason |
|---|--------|
| Kafka/Pulsar between ingest & analysis | Adds latency, ops burden, not needed at demo scale |
| Prometheus remote write | Pull model doesn't fit per-path baselines |
| ClickHouse / TimescaleDB | External dependency; SQLite is sufficient for hot tier |
| gRPC instead of SSE/HTTP | SSE is simpler for single-producer, works over HTTP/1.1 |
| Distributed tracing backend (Jaeger/Tempo) | This *is* the analysis layer; backends store, we analyze |

## Concurrency Model

- `VariantManager` holds 20 `BaselineComputer` instances, each with its own lock
- `UpdateAll` iterates combos sequentially (20 is small); each combo's `AddToRollup` locks only its maps
- API handlers read baselines through snapshots (`GetSnapshot()`), never live maps
- Trace assembly is single-threaded per trace (assembler owns the timer)
- Cold tier queue: 1 writer goroutine, 100-item buffer, backpressure = drop with warning

## Deployment Model

- **Dev**: `docker compose up` (analyzer + web UI, no cold tier)
- **With cold tier**: `docker compose --profile cold up` (adds SeaweedFS)
- **Local Go**: `go run ./cmd/analyzer` + `npm run dev` in web-ui
- **No Kubernetes, no Helm, no secrets manager** — intentionally minimal
