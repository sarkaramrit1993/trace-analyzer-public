# Architecture

One Go process (`analyzer/`) does ingestion, analysis, storage and the HTTP API. A React app (`web-ui/`) reads the API. An S3-compatible store (SeaweedFS in the compose `cold` profile) is an optional cold tier. There's no message bus and no external database.

## Data flow

```
 SSE stream ─┐
             ├─> TraceAssembler ──> pipeline.handle ─────────> SQLite (hot)
 POST /api/  ┘   (per-TraceID       │                          ├─ traces
 spans[/batch]    timer window)     ├─ Fingerprinter.Compute   ├─ deviations / anomalies
                                    ├─ VariantManager.UpdateAll├─ interesting_traces
                                    ├─ CompactionService       └─ rollups (warm) ──> S3 (cold)
                                    │    .AddToRollup
                                    ├─ DeviationDetector.Check ─┐
                                    └─ AnomalyScorer.Score ─────┴─> interesting traces ──> S3 (cold)
```

### 1. Ingestion

`analyzer/internal/ingestion/sse_client.go` connects to `SSE_ENDPOINT` when it's set to something other than empty or `disabled`. It parses `trace_batch` events (a JSON array of spans, or one span object) and hands each span to the assembler. It ignores `start`, `init`, `end` and `query_complete` events and reconnects after 5 seconds on failure.

`POST /api/spans` and `POST /api/spans/batch` in `analyzer/internal/api/server.go` feed the same handler. Each span is added to the assembler inline before the 202 is sent, so nothing is still running after the HTTP server shuts down. Adding a span is an in-memory append; analysis happens later when the trace's timer fires. Request bodies are capped at 1 MB for a single span and 10 MB for a batch (413 above that), and a batch holds at most 1000 spans.

`scripts/continuous-pipeline.py` is an external client that reads Parquet files from S3 and posts batches to `/api/spans/batch`.

### 2. Trace assembly

`analyzer/internal/ingestion/assembler.go`. The first span for a `TraceID` starts a timer of `TRACE_ASSEMBLY_TIMEOUT_SECS` (default 180). Spans are held in memory. A loop that runs every minute writes spans from traces first seen more than a minute ago to the `pending_spans` table, so a long window doesn't hold everything in RAM. Memory tracks at most 10,000 trace IDs; past that, the oldest trace's spans are written to disk and it's dropped from memory. When the timer fires, spans from memory and SQLite are merged (duplicate `SpanID`s keep the latest) and a `models.Trace` is built:

- Root span: the span with an empty `ParentID`, or the earliest span if none has one.
- Duration: latest span end minus earliest span start.
- Error flag: any span whose status is `ERROR`.
- Service ID: from `SERVICE_IDENTITY_FIELDS`, see `computeServiceID`. Grouping values are read with the same `GroupingTagKeys` the fingerprinter uses.

There's no completeness check. A span that arrives after its trace was emitted isn't merged back: it starts a new pending entry under the same `TraceID`, which gets emitted and analyzed on its own once its timer fires.

### 3. Fingerprinting

`analyzer/internal/analysis/fingerprint.go`. Weisfeiler-Lehman refinement over the span tree, 20 iterations, xxhash64 throughout. The initial node label hashes a pipe-joined string of service identity, three scope tags, env, feature group, feature name, sub-service and operation (`spanTopologySignature`). Each value is looked up from the span tags through `models.GroupingTagKeys` (defaults in `DefaultGroupingTagKeys`, overridable with the `*_TAG_KEYS` env vars). Only the values go into the label, so key names don't affect fingerprints: a span tagged `team.scope1=payments` read with `SCOPE1_TAG_KEYS=team.scope1` hashes the same as one tagged `scope1=payments` with the defaults. Labels only flow upward from children, so after k rounds a node's label summarizes its subtree k levels down. The fingerprint is a hash of all node labels, sorted, formatted as 16 hex digits.

Durations, span IDs, timestamps and status are not part of the label, so the fingerprint describes shape only. Sibling order doesn't matter because child labels are sorted. Sibling count does matter: a fan-out of 3 calls and a fan-out of 4 calls to the same service give different fingerprints.

### 4. Baselines and detection

This all happens in `pipeline.handle` in `analyzer/cmd/analyzer/pipeline.go`, once per trace, in this order. `main.go` only wires the pipeline's dependencies, starts the loops and runs shutdown.

1. `compactionService.AddToRollup` adds the trace to the in-memory rollup buffer.
2. `hotStorage.StoreTrace` writes the full trace.
3. If the trace has an error, it's stored as an interesting trace with reason `error` (and sent to cold storage if configured). Error, deviation and anomaly traces all go through `pipeline.recordInteresting`.
4. `analyzeTrace` runs a `DeviationDetector` and an `AnomalyScorer` for **every** one of the 20 combinations against that combination's baseline, then calls `variantManager.UpdateAll(trace, fingerprint)`, which teaches the trace to all 20. Hits are written to `deviations` / `anomalies` tagged with the combination name. Only hits from the active combination are also stored as interesting traces and sent to the cold tier. The deviation and anomaly routes and the per-service issue counts show only the active combination's rows, the same as `/api/stats`.

Detection runs before the update on purpose. Each trace is judged against the baseline as it stood before that trace arrived, so an unseen path still has a count of 0 when it's checked, and a trace's own duration isn't part of the samples it's scored against.

Every combination learns from live traffic, each under its own lock and with its own copy of any imported baseline. See [RESEARCH_QUESTIONS.md](RESEARCH_QUESTIONS.md) for what that does and doesn't make comparable.

Deviation detection (`deviation.go`) returns at most one of:

| Type | Condition (simplified) | Score |
|------|------------------------|-------|
| `new_path` | Path has never been recorded for this service (or, for `same_time_yesterday`, was first seen under 10 minutes of trace time ago and is missing from yesterday's bucket) | 1.0 |
| `frequency_drop` | Path is known-good (ever above 1% of the service's traces) and its share in the current 5-minute window fell by more than 10 points absolute or 50% relative compared to the previous window | `(old - new) / old` |
| `frequency_spike` | Path frequency is below `DEVIATION_THRESHOLD` and its current-window share jumped above 5% | `new - old` |
| `rare_path` | Path frequency is below `DEVIATION_THRESHOLD` and below 0.1% | `1 - freq*100` |

Nothing is checked until the service already has at least min-samples traces, so with `cumulative:50` the 51st trace is the first one checked. Paths seen during that warm-up count as known, so the end of warm-up doesn't produce a burst of `new_path` hits. After warm-up, the first trace with an unrecorded path gets `new_path` and later traces on that path don't. A path whose first-seen time is more than 10 minutes old is skipped for every deviation type until it has min-samples traces of its own. All of these windows, and the 5-minute frequency windows, run on trace time: each combination's "now" is the latest trace start time it has learned, ignoring traces more than 5 minutes ahead of the wall clock (`MaxFutureSkew`). A late trace is stamped with "now" rather than its own start time, so a new path it brings in stays new for the full 10 minutes. Ingest speed doesn't change results. Note that `DEVIATION_THRESHOLD` is a frequency cutoff for "rare", not a score threshold.

Anomaly scoring (`anomaly.go`) works on the path baseline for the trace's fingerprint, once that path has at least min-samples traces:

```
spread = (P_ref - P25) / 1.35
score  = | 0.6745 * (duration - P_ref) / spread |
```

`P_ref` is the `ANOMALY_BASELINE_PERCENTILE` percentile (default 0.75) of the path's duration samples. With the default this is centered on P75 and uses the interquartile range scaled to a standard-deviation estimate. It is not a median absolute deviation. The absolute value means unusually fast traces also score high. If the spread is zero the code falls back to `(duration - P_ref) / ((max - min) / 4)` with no absolute value. A trace is an anomaly when `score >= ANOMALY_ZSCORE_THRESHOLD` (default 3.5).

For anomalous traces, `identifySlowBranches` scores each parent/child edge (`parentSvc:parentOp->childSvc:childOp`) against the service's branch baseline with the same formula. Edges with at least 10 samples and a score above 2.0 are ranked by excess over the edge's median, and the top 5 are reported as `slow_branches`.

## Why per-path stats need multi-key storage

A per-service baseline would key everything by service ID. This one can't, because the point is to compare a trace to other traces with the same shape. So every statistic carries at least two keys, and some carry more:

| Structure | Key | Where |
|-----------|-----|-------|
| Path counts, first-seen, known-good set | service ID, then fingerprint | `ServiceBaseline.TopologyCounts` etc. in `analysis/baseline.go` |
| Path duration baseline (count, sum, min, max, last 1000 samples) | service ID, then fingerprint | `ServiceBaseline.PathBaselines` |
| Edge duration baseline (last 100 samples) | service ID, then edge key `parentSvc:parentOp->childSvc:childOp` | `ServiceBaseline.BranchBaselines` |
| Same-time-yesterday history: per hour and service, trace total and path counts; per path exact count, sum, min, max and a reservoir of up to `HOURLY_PATH_SAMPLE_CAP` durations | UTC hour `YYYY-MM-DD-HH`, then service ID, then fingerprint | `HourlyHistory` in `analysis/hourly.go`, one shared by the four `same_time_yesterday` combinations (`VariantManager.hourly`) |
| All of the above | combination name, e.g. `ewma:100` | `VariantManager.combinations` in `analysis/variant.go` |
| Warm rollup | service ID, fingerprint, 15-minute window start | in memory as `serviceID:fingerprint:windowStart`; in SQLite `PRIMARY KEY (window_start, service_id, fingerprint)` |
| Deviation / anomaly rows | service ID, plus a `variant` column | `deviations`, `anomalies` tables |

The rollup key follows from the same idea. If rollups were keyed only by service and time window, the per-path duration distribution would be mixed together and you couldn't rebuild a path baseline from history. Keeping fingerprint in the key costs one row per path per window per service, so the row count grows with the number of distinct paths, not just the number of services.

One mismatch to be aware of: rollup `branch_stats` are keyed per span as `service:operation`, while baseline branch stats are keyed per edge. The two can't be joined directly.

Only cold compaction reads rollups back (`GetRollupsBeforeCutoff`). `ColdTierStorage.ReadRollups` exists, but no API route or startup path calls it.

Shutdown flushes the open window, so after a restart the same window can be flushed a second time. `StoreRollup` merges into the stored row instead of replacing it: counts, sums and branch stats add, min and max combine, sample trace IDs are unioned (up to 10). P50 and P99 can't be merged from two summaries, so they become the trace-count-weighted mean of both parts, which is exact only when both parts share a distribution.

## Storage interfaces

Defined in `analyzer/internal/storage/sqlite.go`:

- `Storage`: traces (store, get, recent, by fingerprint), deviations and anomalies (store with a variant name, query by service with limit and min score), rollups (store, merging into an existing row for the same key), `Cleanup`, `Stats`, `StatsForVariant`, `Close`.
- `ServiceIssueCountsProvider`: `GetServiceIssueCounts(variant)`, used by the services routes to attach deviation and anomaly counts for the active combination. An empty variant counts every combination, and the same goes for the `variant` argument of `GetDeviations` / `GetAnomalies`.
- `InterestingTraceStore`: store, list and fetch error/deviation/anomaly traces.

The API server holds a `storage.Storage` and type-asserts for the two side interfaces, returning 501 from the interesting-trace routes if they aren't implemented. `/api/stats` type-asserts to `*storage.SQLiteStorage` directly for per-variant counts.

`SQLiteStorage` is the only implementation. It also has methods outside the interfaces that `main.go` and the assembler call on the concrete type: pending span persistence, baseline persistence (`SaveCombinationBaselines`, `CombinationBaselineKeys`, `ForEachCombinationBaseline`, `SaveHourlyBaselines`, `LoadHourlyBaselines`, plus the legacy `LoadBaselines`) and rollup compaction helpers.

## Tiering and retention

`analyzer/internal/storage/compaction.go` runs three loops, started from `main.go` with a 15-minute rollup interval:

| Loop | Interval | What it does |
|------|----------|--------------|
| Hot cleanup | 1 minute | `SQLiteStorage.Cleanup(HOT_RETENTION_MINUTES)`: deletes non-important traces older than the retention, and important traces, deviation/anomaly rows and `interesting_traces` rows older than 10x the retention. Cutoffs are computed in UTC to match SQLite's `CURRENT_TIMESTAMP`; `interesting_traces` ages by the trace's start time |
| Rollup flush | 15 minutes | Writes finished windows from memory to the `rollups` table with P50/P99 from up to 1000 reservoir-sampled durations per window. On shutdown the window still in progress is written too, and a later flush of that window merges into it |
| Cold compaction | 1 hour, only when cold storage is configured | Reads rollups whose `window_end` is older than `WARM_RETENTION_MINUTES`, writes them as gzipped JSON under `rollups/YYYY/MM/DD/HH/`, then deletes them from SQLite |

A trace becomes "important" when it's stored as an interesting trace (error, or a deviation/anomaly from the active combination). `interesting_traces` is keyed on `(trace_id, reason)`, so an error trace that is later flagged as an anomaly keeps both rows; older databases keyed on `trace_id` alone are rebuilt with the new key on startup. Interesting traces are also written straight to the cold tier under `interesting-traces/YYYY/MM/DD/HH/` through a buffered queue (capacity 100) drained by one goroutine. When the queue is full the record is dropped with a warning, so a slow bucket can't stall trace processing.

Things the retention logic doesn't do:

- Without a cold tier, warm rollups are never deleted.
- Nothing expires objects in the cold bucket. `ColdRetentionHours` exists in `models.Config` but nothing reads it.

### Baseline persistence

Baselines live in memory. Every 5 minutes, and on shutdown, `saveAllBaselines` in `main.go` writes them to SQLite. Unless `CLEAN_START` is true, startup loads them back before any trace is processed.

Tables:

| Table | Key | Contents |
|-------|-----|----------|
| `baseline_combinations` | `(service_id, combination)` | One JSON row per learned combination and service. Written in one transaction per save, one row at a time, so memory stays flat. A failed save rolls back and the previous save stays intact. |
| `hourly_baselines` | `(hour, service_id)` | The shared `same_time_yesterday` hourly buckets. Each save upserts only buckets that changed since the last save and deletes hours no lookup can reach any more (`RetainedHourKeys`). Written in its own transaction after the combination rows, so a failure in one doesn't roll back the other. If an hourly save fails, the next save writes every bucket again. Skipped when no `same_time_yesterday` combination is learned. |
| `baselines` | `service_id` | Legacy: the active combination only, written by builds before per-combination persistence. Read as a fallback, never written. |
| `settings` | `key` | Small key/value store. `active_combination` holds the combination last chosen through the API, written by the API handler right after the switch (`SaveActiveCombination`). Startup reads it in `resolveActiveCombination`: an explicitly set `ACTIVE_COMBINATION` wins, then this value if still learned, then `cumulative:50`, then the first learned. |

Load rules, per service and learned combination:

1. Its own `baseline_combinations` row.
2. Otherwise the stored row of the same variant with the lowest threshold, for example after `LEARN_COMBINATIONS` adds `ewma:50` next to a stored `ewma:100`.
3. Otherwise, only if the service has no `baseline_combinations` row of that variant, the legacy `baselines` row. This is the upgrade path; once a variant has a saved row, its combinations use that row or a sibling. A variant first learned after the upgrade (say all 20 after running only `ewma:500`) still gets the legacy row.
4. Otherwise it starts empty.

Rows for combinations that aren't learned and aren't needed as a sibling, rows with unknown combination names, and rows that fail to decode are ignored. The startup line `Loaded persisted baselines` logs the own, sibling, legacy, ignored and corrupt counts plus the hourly bucket count. Each save logs rows, bytes and duration (Info at shutdown, Debug in the 5-minute loop).

Size and cost: a busy service with 32 paths of 1000 samples and 16 branches is about 217 KB per combination, so about 4.3 MB for all 20. Saving 20 such services across 20 combinations (400 rows, 87 MB) takes about 0.2 s as an update and 0.4 s on first write on a development laptop (`BenchmarkSaveAllBaselines`, `TestPersist_SaveFitsShutdownBudget` keeps it under 3 s). The WAL grows to the size of the largest save transaction.

Not persisted: `TopologyExamples` (refilled as traces arrive), the 5-minute `RecentWindow` and `PreviousFrequencies` used by the frequency-shift check (they start empty and the first window opens at the first trace after restart). The active combination is persisted, in `settings`. `TopologyFirstSeen` and per-path `LastUpdateTime` hold trace times, so the new-path window and the decay ages compare like with like after a restart. Every export carries `"clock":"trace"` to say so. Rows without it were saved before that change and may hold wall-clock first-seen times, so on import their stamps aren't trusted: every path in the row gets the zero first-seen, which `IsNewPath` reads as seen long ago (it was in the saved row, so it isn't new; a path seen in the last 10 minutes before such a save loses the rest of its new-path window). For `sliding_window` and `exponential_decay` the last-seen used for decay comes from the path's `LastUpdateTime` when the row has one; otherwise the path gets no last-seen until the first trace after the restart, which stamps it, so decay starts there instead of from a wall-clock age. The `same_time_yesterday` hourly rows need no marker: their hour keys have come from trace `StartTime` since the table was added. Everything else in a combination's service baseline round-trips, including per-path `LastUpdateTime`, `ServiceGrouping` and the overall `DurationSamples`. Rows written before `DurationSamples` was saved rebuild it from up to 10 samples per path.

### Shutdown

On SIGINT or SIGTERM, `main` stops things in this order: HTTP server, SSE client, trace assembler (which flushes every pending trace through the handler), cold-tier queue (closed, then drained), the periodic baseline and stats loops, the final baseline save, compaction (which writes open rollup windows) and finally SQLite. The handler still runs during the assembler flush, so everything it writes to must still be open at that point.
