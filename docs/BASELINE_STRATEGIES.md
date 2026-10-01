# Baseline strategies

A baseline is what the detectors compare each trace against. It's kept per service, and inside that per path (fingerprint) and per edge. There are five ways of maintaining it, called variants. Code: `analyzer/internal/analysis/variant.go` and `analyzer/internal/analysis/baseline.go`.

## What every variant shares

`VariantManager.UpdateAll` hands each trace to every learned combination. Whatever the variant, each combination's `BaselineComputer` does the following:

- Increments the path's count in `TopologyCounts` and the service's `TotalTraces`, and records a first-seen time and an example trace for new paths.
- Adds the path to a 5-minute rolling window used for frequency-shift checks (`updateRecentWindow`).
- Once `TotalTraces` reaches min samples, marks any path above 1% of traces as known-good (never unmarked) and picks the most common path as canonical if it's above 10%.
- Updates the path duration baseline. Every variant keeps up to 1000 duration samples per path, allocated as needed, so a rarely seen path holds a few slots rather than 1000. When full, new samples overwrite slot `Count % 1000`. The code comments call this reservoir sampling but it's a ring buffer indexed by count.
- Updates edge baselines keyed `parentSvc:parentOp->childSvc:childOp`, with a 100-sample ring buffer, also allocated as needed.

`main.go` runs detection before this update, so checks see the baseline without the current trace in it.

### Trace time, not wall time

Every time-based rule in detection runs on trace time. Each `BaselineComputer` keeps its own "now", the latest trace `StartTime` it has learned. First-seen and last-seen stamps are trace start times for every variant, except that a stamp never goes behind "now": a late trace (start time before "now") is stamped with "now". So a path first seen on a late trace still gets the full 10-minute new-path window, and decay ages never go negative. The 5-minute recent window rotates when a learned trace is 5 minutes past the window start, and `IsNewPath` measures the 10-minute new-path window from the later of "now" and the checked trace's own start time. So replaying old data, or ingesting a backlog in seconds, gives the same results as live traffic.

One guard reads the wall clock. A trace whose start time is more than `MaxFutureSkew` (5 minutes) ahead of the wall clock is still learned, but it doesn't advance "now", doesn't move hourly retention, and is stamped with the current "now". If it's the first trace that combination has learned, so there is no "now" yet, it's stamped with the wall clock instead. Without that, one trace from a host with a broken clock would make every path look old and push same-time-yesterday retention past the real history. `LastSeen` and the service-level `LastUpdateTime` stay wall-clock bookkeeping for the API and are not read by detection.

Anomaly scoring reads percentiles from the path's sample buffer and gates on the path's `Count`. Deviation detection reads `TopologyCounts / TotalTraces`, the known-good set, first-seen times and the 5-minute windows. So the variants differ in how they bend `TopologyCounts`, `Count` and `SumDuration`. The percentile samples that drive anomaly scores are handled the same way everywhere.

## The five variants

### cumulative

Nothing decays. Counts and sums grow for the life of the process (and across restarts through persisted baselines).

- Memory horizon: everything since the baseline started. Percentiles come from the last 1000 samples per path.
- Adaptation: slow for frequencies, since a shift has to outweigh all past traffic. Anomaly percentiles adapt as the 1000-sample buffer turns over.
- Stability: the most stable.
- Default min samples: 50.

### ewma

- Topology frequency: `TopologyFrequenciesEWMA[fp] = 0.1 + 0.9 * previous`, updated only when that path appears. This map is persisted but no detector reads it. Deviation checks still use `TopologyCounts / TotalTraces`, so frequencies behave like `cumulative`.
- Path duration: `SumDuration` is rewritten so that `SumDuration / Count` tracks an EWMA of duration with alpha 0.1. That mean shows up in the API. Anomaly scoring uses percentiles from samples, not the mean.
- Memory horizon: roughly the last 10 to 20 traces for the mean. Percentiles still use the last 1000 samples.
- Adaptation: fast for the mean, same as `cumulative` for detection.
- Stability: in practice close to `cumulative` for flags.
- Default min samples: 500.

### sliding_window

Window length is `SLIDING_WINDOW_DURATION` (24h by default, set from `main.go`; `NewBaselineComputer` alone would default to 1h).

- Topology counts: on each trace, every other path's count is multiplied by `1 - 0.1 * age/window`, where age is time since that path was last seen, measured against the trace's start time. Paths unseen for longer than the window drop to 0 and are deleted after two windows. The current path is incremented twice, once by the shared update and once here. `TotalTraces` doesn't decay, so frequencies computed as `count / TotalTraces` shrink over time.
- First-seen and last-seen: tracked separately. Decay ages come from a last-seen map, first-seen is the path's real first sighting, so `IsNewPath` behaves like the other variants. A path deleted after two windows unseen loses both and is reported as new again when it returns. After a restart last-seen is rebuilt from each path's `LastUpdateTime`.
- Path duration: `Count` and `SumDuration` are scaled by up to 5% per window of age, or halved if the path hasn't been seen for a full window.
- Memory horizon: about one window for counts. Percentiles still use the last 1000 samples.
- Adaptation: faster than `cumulative`, but the decay applies per trace, so its strength depends on traffic volume, not just time.
- Stability: moderate. Busy services decay faster than quiet ones.
- Default min samples: 50 (the default branch of `GetVariantMinSamples`).

### exponential_decay

Half-life is 24h (`SetExponentialDecayHalfLife` in `variant.go`).

- Topology counts: on each trace, every other path's count is multiplied by `exp(-ln2 / halfLife * age)` where age is time since that path was last seen. Counts are integers, so small counts truncate toward 0. The current path is incremented twice. Like `sliding_window`, ages come from a separate last-seen map (persisted through the path's `LastUpdateTime`), first-seen stays the first sighting, and `TotalTraces` doesn't decay.
- Path duration: every 100th trace for a path, `SumDuration` is multiplied by 0.95, and `Count` too once it's over 100.
- Memory horizon: about a day for counts, in theory. Percentiles still use the last 1000 samples.
- Adaptation: depends on traffic, same caveat as above.
- Stability: the per-trace decay compounds, so a path that's quiet for a while can drop sharply in one busy burst.
- Default min samples: 5000.

### same_time_yesterday

Code: `analyzer/internal/analysis/hourly.go`.

The current baseline (counts, known-good set, first-seen, path and edge baselines) is maintained like `cumulative`. On top of that the variant keeps an hourly history.

- Buckets: one per UTC hour (`YYYY-MM-DD-HH`) and service. Each holds `TotalTraces`, a count per path, and per path the exact `Count`, sum, min and max duration plus a sample of durations. The sample is a reservoir (Algorithm R) capped at `HOURLY_PATH_SAMPLE_CAP`, 200 by default, so it stays a uniform sample of the hour however busy the path is. The reservoir uses a fixed seed, so a replay of the same traces keeps the same samples.
- Shared: the four `same_time_yesterday` thresholds read one history, which `VariantManager.UpdateAll` feeds once per trace. If no `same_time_yesterday` combination is learned, no history is allocated.
- Comparison window: for a trace at time t, the buckets for yesterday's same hour ±2h, five hours in all. If yesterday was Saturday or Sunday, Friday's hours are used instead (`comparisonHourKeys`).
- Deviation check: path frequency is `count / TotalTraces` from the bucket in the window with the most traces. Without any bucket it falls back to the current baseline. Everything else in the check (new-path test, known-good set, 5-minute shift windows) uses the current baseline.
- Anomaly check: the path's buckets across the whole window are merged into one fresh duration baseline (counts and sums added, min and max combined, samples concatenated, so up to 5 x 200 samples). If the merged `Count` reaches the combination's min samples, the trace is scored against it. Otherwise it's scored against the current path baseline, like `cumulative`. Each hour keeps at most 200 samples, so a busy hour's samples each stand for more traces than a quiet hour's. When more than one hour contributes, each sample is weighted by its hour's `Count / len(Samples)` and the percentiles are weighted percentiles: the smallest sample whose cumulative weight passes rank `(W-1) x pct` of the `W` weighted traces. With every weight 1 (no hour over the cap, or a single hour) that is exactly the plain `sorted[int((n-1) x pct)]` rule used everywhere else. So an hour of 10,000 traces at about 1000 ms next to an hour of 50 at about 100 ms gives a P50 of about 1000 ms, not the 100 ms the unweighted merge produced. `Count`, sum, min, max and mean are exact either way.
- Retention: buckets are kept while some lookup in the next 4 days can still reach them (`retainedHourKeys`). That's at most 105 hours, for example Friday from 08:00 is kept until Monday's 10:00 lookups are done. Retention follows the newest trace time seen, not the wall clock, so replaying old data keeps its own "yesterday". Cleanup runs when the newest trace moves into a new hour. Traces for an hour no lookup can reach (most of Saturday and Sunday, or late traces for an hour already dropped) aren't stored. A trace more than `MaxFutureSkew` (5 minutes) ahead of the wall clock doesn't move retention, and neither does a persisted bucket that far ahead on import, so one bad timestamp can't drop the real history.
- Memory: at most `cap x 8` bytes of samples per (hour, service, path), 1.6 KB at the default cap, so at most about 168 KB per (service, path) across 105 retained hours. Samples are allocated as needed: 5000 paths seen 3 times in one hour use about 320 KB of sample space. The stats log line reports `hourly_buckets` and `hourly_samples`.
- Persistence: the hourly history is saved to the `hourly_baselines` table with the other baselines (every 5 minutes and on shutdown, changed buckets only) and loaded at startup, so a restart keeps yesterday's buckets. Hours past retention are deleted from the table on each save.
- Adaptation: resets daily by design. Needs a full day of traffic before it compares anything to "yesterday".
- Stability: sensitive to daily traffic shape. A restart loses at most the traffic since the last save.
- Default min samples: 50.

## Combinations

Each variant runs with each threshold in `AvailableThresholds`: 50, 100, 500, 5000. That's 5 x 4 = 20 `BaselineComputer` instances, named `<variant>:<threshold>`, e.g. `sliding_window:500`. The threshold is the min-samples gate: a service needs that many traces before deviation checks run, and a path needs that many before it's scored for anomalies or considered for non-new deviations.

There is no global min-samples setting. The old `MIN_SAMPLES_FOR_BASELINE` variable is no longer read; if it's set, the analyzer logs a warning at startup and ignores it.

By default all 20 combinations learn. `LEARN_COMBINATIONS` narrows that to a subset to save memory and CPU: `all` (or unset) learns all 20, `active` learns only the `ACTIVE_COMBINATION`, and a comma-separated list such as `cumulative:50,ewma:500` learns exactly those. Each name must be a known variant with a threshold from the list above; duplicates are dropped. An invalid value stops the analyzer at startup with the valid names in the error, the same as an invalid `ACTIVE_COMBINATION`. Combinations that aren't learned don't exist at all: they aren't checked, aren't listed by the API, and can't be made active.

A bare variant name means `<variant>:<GetVariantMinSamples(variant)>` (`analysis.DefaultCombination`). `GET /api/variants/:name` accepts either form: a bare variant such as `ewma` resolves to `ewma:500`, and a full combination such as `ewma:100` resolves to itself. The response reports the resolved pair as `variant` and `combination`, plus `info` and the number of `services` that combination has learned.

## What runs on each trace

From `main.go`:

1. Deviation and anomaly checks run for every learned combination (all 20 by default), each against its own baseline, and results are stored with the combination name in the `variant` column.
2. Every learned baseline is then updated with the trace (`VariantManager.UpdateAll`).
3. Only the active combination's hits become interesting traces and go to the cold tier.

So every combination learns from the same live traffic and flags against its own history. The deviation and anomaly lists and the per-service issue counts in the API show only the active combination's rows; the other 19 are in the same tables under their own `variant` names. Each combination's baselines are saved under its own name, so after a restart every combination resumes its own state. A combination with no saved row of its own (for example one just added to `LEARN_COMBINATIONS`) starts from the same variant's lowest-threshold saved row, or empty. A database written by an older build, which saved only the active combination, seeds all 20 from that row once; after the next save each combination has its own. See the persistence section of `ARCHITECTURE.md`.

## Switching

| Call | Effect |
|------|--------|
| `GET /api/variants/combinations` | Lists the learned combinations (all 20 by default) with metadata and the active one |
| `POST /api/variants/combination` with `{"combination":"ewma:100"}` | Makes that combination active. An unknown or not learned name returns 400, with a message pointing at `LEARN_COMBINATIONS`, and leaves the active combination unchanged. |
| `POST /api/variants/active` with `{"variant":"ewma"}` | Makes `<variant>:<GetVariantMinSamples(variant)>` active, e.g. `ewma:500`, `exponential_decay:5000`. An unknown variant, or one whose default combination isn't learned, returns 400. |
| `GET /api/variants/active` | Shows the active combination |

The UI header has variant and threshold dropdowns that call the combination endpoint.

Switching takes effect immediately. The newly active combination has been learning all along, so its baselines are already warm (subject to its own min-samples gate). The services list, topologies, deviation and anomaly lists, issue counts and `/api/stats` counts all come from the active combination. A switch made through the API is saved in SQLite and survives restarts. On startup the active combination is, in order: `ACTIVE_COMBINATION` if it's set, else the last combination chosen through the API if it's still learned, else `cumulative:50` if learned, else the first learned combination. `ACTIVE_COMBINATION`, when set, must name a learned combination: an unknown name, or one left out of `LEARN_COMBINATIONS`, stops the analyzer at startup with the list of valid learned combinations. A saved choice that is no longer learned only logs a warning and falls back. See the runbook for details. The startup log lists the learned set and the active combination.
