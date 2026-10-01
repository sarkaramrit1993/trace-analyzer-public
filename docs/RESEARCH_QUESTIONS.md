# Research questions

These are open questions about whether the analysis does what it's meant to. Some are genuine research problems. Others are behaviours found by reading the code that look unintended. Each points at the code involved so it can be checked or fixed.

## 1. How accurate is any of this?

Nothing here has been validated against real incidents. There's no labelled dataset, no precision or recall numbers, and no comparison against a simple per-service latency alert. The unit tests check that the code runs and produces plausible output on synthetic traces. They don't say whether a flag means something happened.

What a first validation could look like:

- Replay a period of real spans through `/api/spans/batch` where known incidents or deploys happened, and check whether deviations or anomalies cluster around them.
- Inject synthetic changes (add a hop, slow one edge, shift the path mix) into replayed traffic and measure detection rate and delay per combination.
- Measure false positive rate on quiet periods.

Replay gives the same results as live traffic. Detection used to mix wall-clock and trace time: `IsNewPath` compared first-seen with `time.Since`, `cumulative`, `ewma` and `same_time_yesterday` stamped first-seen with the wall clock, and the 5-minute recent window rotated on the wall clock. During a fast replay every path counted as new for 10 real minutes (so the min-samples gate was skipped) and the recent window never rotated. Now first-seen, the new-path window and the recent window all use trace time (see "Trace time, not wall time" in [BASELINE_STRATEGIES.md](BASELINE_STRATEGIES.md)). Results recorded from a replay or a fast backfill before that change should be re-checked. On the golden corpus the change removed 130 of 258 findings and added none: 118 repeated `same_time_yesterday` `new_path` hits on paths missing from yesterday's bucket, and 12 `rare_path` hits from `cumulative` and `ewma` on rare paths first seen hours earlier. Every genuine first sighting is still flagged.

## 2. Do new paths get flagged?

Yes, since `analyzeTrace` in `analyzer/cmd/analyzer/main.go` started checking before it updates. It used to update the active combination's baseline first, so by the time `DeviationDetector.Check` ran the path's count was already 1, its frequency was above 0, and the `new_path` branch (`isNew && freq == 0`) could never fire. A new path could only show up as `rare_path`, which needs a frequency under 0.1%, so a service had to have seen more than 1000 traces first. Deviation and anomaly results recorded before the fix should be re-checked.

What happens now, for the active combination:

- While the service has fewer than min-samples traces (50 for `cumulative:50`), nothing is checked. Every path seen in that window is recorded and won't be reported as new later.
- After that, the first trace whose fingerprint has never been recorded for the service gets exactly one `new_path` deviation with score 1.0.
- The next trace on that path isn't new any more. It can still be reported as `rare_path` if its frequency is under 0.1% and it was first seen under 10 minutes of trace time ago.
- `same_time_yesterday` is the exception. Its frequency comes from yesterday's bucket, so a path that bucket lacks is reported as `new_path` on every occurrence during its first 10 minutes of trace time. That reads as "new compared to yesterday".

The 10-minute rule in `IsNewPath` doesn't hold back genuinely new paths, because a path with no first-seen record always counts as new. What it does: once a path's first-seen time is more than 10 minutes old, `Check` skips that path for every deviation type until it has min-samples traces of its own. It works as a grace window. A path gets 10 minutes in which it can be reported as rare, and after that it's ignored until it has enough data. The effect is that a path which keeps showing up a few times an hour is never reported again. Whether that's the right trade between noise and recall is still open. All variants measure the 10 minutes from the path's first sighting in trace time. `sliding_window` also forgets a path entirely after two windows without it, after which it counts as new again. (Before the trace-time change, `sliding_window` and `exponential_decay` overwrote first-seen with last-seen, so the window restarted each time a path showed up.)

`TestAnalyzeTrace_*` in `analyzer/cmd/analyzer/main_test.go` pins these behaviours.

## 3. Are the non-active combinations comparable at all?

They are closer now. All 20 combinations are checked on every trace and then all 20 learn from it (`VariantManager.UpdateAll`), so each one flags against its own history of the same traffic. Imported baselines are deep-copied per combination, so they no longer share maps. After a restart, a new path is reported once on each combination that is past its own min-samples gate, and not again, because each combination has now learned it.

What still limits the comparison:

- Every combination's baselines are now persisted under its own name, so a restart no longer collapses the 20 into the active one. What still resets: the 5-minute frequency-shift windows and the example trace per path.
- The API lists and counts show the active combination only. Comparing combinations means querying the `deviations` / `anomalies` tables by `variant` directly, or switching the active combination.
- Cost: every trace now updates 20 baselines, including the per-trace decay loops over every path in `sliding_window` and `exponential_decay`. This hasn't been measured under load.

## 4. What does same_time_yesterday actually compare?

From `analyzer/internal/analysis/hourly.go`, `anomaly.go` and `deviation.go`:

- For anomalies, a path is scored against yesterday's ±2 hour window once that window holds min-samples traces of the path, merged from per-hour reservoirs of up to 200 samples. Below that it's scored against the current baseline, like `cumulative`. Until this change the hourly buckets held no durations and the variant always behaved like `cumulative` for anomalies, so older `same_time_yesterday` anomaly rows reflect that.
- At the higher thresholds (500, 5000) a path needs that many traces in a five-hour window yesterday, so most paths fall back to the current baseline. Whether those combinations are worth keeping is open.
- Merged percentiles are weighted by each hour's true `Count`, so a busy hour counts for its real share of traffic. Before that fix every hour weighed the same whatever its volume, which pulled percentiles toward quiet hours; `same_time_yesterday` anomaly rows written before it reflect that bias where an hour of the window went over the 200-sample cap.
- For deviations, only the frequency comes from yesterday. The known-good set, the new-path check and the 5-minute frequency shift checks all use the current baseline.
- The frequency bucket chosen is the one with the most traces in the ±2 hour range, not the one closest to the current hour. The anomaly side merges the whole range instead.
- Weekend handling: on Sunday and Monday, "yesterday" becomes Friday. On Saturday, yesterday is Friday anyway. Saturday and Sunday traffic is never used as a comparison, and weekend traffic is compared against Friday's.
- The history is persisted (`hourly_baselines`), so a restart keeps yesterday's buckets. A fresh database still has nothing to compare against for a day and silently uses the current baseline.
- Everything is in UTC, so "same time" ignores the local business day of the service.

Open question: is day-over-day comparison useful for path mix at all, or does it mostly re-detect daily traffic shape?

## 5. How should deviation and anomaly scores relate?

The two detectors run independently and store separate rows. Their scores aren't on the same scale:

- Deviation scores are 1.0 for new and rare paths (`1 - freq*100` is above 0.9 whenever it fires) or a fraction for frequency shifts.
- Anomaly scores are unbounded, with a default cutoff of 3.5.

A trace can be both: a rare path whose path baseline also shows it as slow. There's no combined view. `interesting_traces` keeps one row per trace and reason, so such a trace appears once per reason, and the UI lists them separately.

Related questions:

- The anomaly score is `|0.6745 * (x - P_ref) / ((P_ref - P25) / 1.35)|`. With the default `P_ref = P75`, that's distance from P75 in IQR-derived sigma units, scaled by 0.6745. The 0.6745 constant belongs to the median-absolute-deviation form of the modified z-score, and the 1.35 constant converts an IQR to a sigma. Using both together gives a score about 0.67 times a plain IQR-based z-score. Was that intended, and was 3.5 tuned with it in mind?
- The absolute value flags unusually fast traces as anomalies. Fast can mean a skipped step, which may be interesting, but it's reported as "slow branches".
- When `ANOMALY_BASELINE_PERCENTILE` is set to something other than 0.75, `P_ref - P25` isn't an IQR any more. Lower values shrink the spread, and below 0.25 it goes negative.
- The zero-spread fallback `(x - P_ref) / ((max - min) / 4)` is not an absolute value, unlike the main formula.
- Slow branch attribution uses a fixed cutoff of 2.0 and ignores edges with fewer than 10 samples.

## 6. Is the fingerprint the right granularity?

`analyzer/internal/analysis/fingerprint.go`.

- **Collisions.** Fingerprints are 64-bit. For the number of distinct paths a single service produces, accidental collisions are very unlikely, but nothing detects them. Two paths that collide would share a baseline without anyone noticing.
- **Fan-out.** Sibling count is part of the shape. A loop that calls a service once per item gives a different fingerprint for every item count, which fragments baselines and can make normal traffic look like a stream of rare paths. Collapsing repeated identical children could help, at the cost of hiding real changes in call count.
- **Labels.** The node label includes env and several scope and feature tags. If those vary per request, the same code path splits into several fingerprints. Operation names with IDs embedded (for example raw URLs) do the same.
- **Depth.** 20 iterations means each node's label covers 20 levels of its subtree. Since the final hash covers every node's label, differences deeper than that are still visible through nodes nearer to them, so for trees the iteration count may matter less than the comment suggests. Worth confirming with a test on deep traces, and measuring the cost at typical span counts.
- **Broken traces.** Spans whose parent never arrived become extra subtree roots. A trace missing one span gets a different fingerprint from the complete version, so late or dropped spans show up as rare paths. The assembler emits whatever arrived within `TRACE_ASSEMBLY_TIMEOUT_SECS`, and a late span starts a separate trace with the same ID.
- **Async work.** `models.Span` assumes synchronous parent/child links. Work handed off through queues shows up as separate traces, so a path change across a queue boundary isn't visible.

## 7. Do the decaying variants decay the right thing?

`sliding_window` and `exponential_decay` decay `TopologyCounts` but not `TotalTraces`, so path frequencies drift toward 0 over time and more paths look rare. Their decay runs on every trace for every other path, so the effective memory depends on traffic volume. And percentiles for anomaly scoring come from the same 1000-sample buffer in every variant. It's not clear these variants currently test the idea their names describe. See [BASELINE_STRATEGIES.md](BASELINE_STRATEGIES.md).

## 8. What's the right min-samples gate?

The thresholds 50, 100, 500 and 5000 aren't derived from anything. The gate applies both per service and per path, and the per-path gate is what decides when a path becomes scorable. A principled choice would depend on how stable P25 and P75 estimates are at a given sample size for typical latency distributions, which could be measured from replayed data.
