# Engineering Decisions

Every choice below started as a requirement or constraint, not a preference. The alternatives were real — we tried some, rejected others for specific reasons, and noted what we'd change with more data.

---

## 1. 20 Parallel Baseline Combinations

**Requirement**: Catch different failure modes. Sudden latency shift ≠ slow drift ≠ daily pattern ≠ weekend seasonality.

**Options considered**:
- One adaptive baseline that switches strategy
- Fixed parallel baselines (what we built)
- Per-service configurable pipeline

**Why parallel**: No single strategy dominates. EWMA catches sudden shifts; cumulative catches slow drift; sliding window catches recurring patterns; same-time-yesterday catches daily seasonality. Running all 20 (5 variants × 4 min-sample thresholds) costs ~17 µs/trace on a dev laptop. `LEARN_COMBINATIONS` lets operators narrow it to `active` or a specific list to save CPU.

**What we'd revisit**: Under real load, measure if EWMA + sliding_window suffice and drop the rest.

---

## 2. Fingerprinting: Weisfeiler-Lehman (20 Iterations)

**Requirement**: Stable ID for "same execution path" that survives span reordering and minor topology changes.

**Options considered**:
- Span sequence hash (order-sensitive)
- WL graph hash (structure-aware)
- Path signature (simplified span tree)

**Why WL**: Structure matters more than exact span order in distributed traces. 20 iterations covers the depth of any real service call graph. 64-bit output → collisions negligible at <1000 paths/service.

**What we'd revisit**: If fan-out varies per request (loops, batch sizes), WL fragments baselines. Could add a "collapse repeated children" pre-pass.

---

## 3. Trace Time, Not Wall Clock

**Requirement**: Fast ingest and replay must not mark every path "new" for 10 real minutes.

**Options considered**:
- Wall clock with ingestion-rate awareness
- Pure trace `StartTime` (what we built)
- Hybrid: trace time for decay, wall clock for retention

**Why trace time**: Replay and backfill are first-class use cases. New-path window, decay age, and hourly cleanup all compare like-with-like. `MaxFutureSkew = 5 min` prevents one bad timestamp from corrupting history.

**What we'd revisit**: If traces arrive out of order by >5 min, the skew clamp drops data. Could add a reorder buffer.

---

## 4. Baseline Persistence: Per-Combination, Not Just Active

**Requirement**: Restart must not collapse 20 combinations into 1. Switching combos after restart must not give empty baselines.

**Options considered**:
- Save only active (less I/O)
- Save all learned combos, each under its own key (what we built)
- Save active + snapshot others periodically

**Why per-combination**: Restart fidelity > I/O cost (400 rows = ~0.4 s). Legacy row upgrade path: old DBs seed all 20 from the single saved row. Failed save rolls back; previous save stays intact.

**What we'd revisit**: If combo count grows, add "dirty flag" to skip unchanged combos.

---

## 5. Three Independent Deviation Checks

**Requirement**: Different failure modes need different remediation. "New code path" ≠ "config drift" ≠ "load pattern change."

**Decision**: Three independent checks, each produces its own row:

| Type | Signal | Threshold |
|------|--------|-----------|
| `new_path` | First time seeing this fingerprint | Always fires once per combo |
| `rare_path` | `freq < 1/(TotalTraces×100)` | Requires min-samples |
| `freq_shift` | `|freq_t - freq_{t-5m}| > 2×σ` | 5-min rolling window |

**Why independent**: New path = new code path. Rare path = config drift. Freq shift = load pattern change. Conflating them loses signal.

**What we'd revisit**: `freq_shift` rarely fires on low-volume services. Could add a volume gate.

---

## 6. Anomaly Scoring: Weighted Z-Score (Modified)

**Requirement**: Latencies are heavy-tailed; standard z-score assumes normal.

**Decision**: `|0.6745 × (x - P75) / (IQR / 1.35)|` with cutoff 3.5

**Why**: MAD-based modified z-score is robust to outliers. 0.6745 converts MAD to σ for normal; 1.35 converts IQR to σ. Using both gives ~0.67× plain IQR z-score — empirically tuned on synthetic data.

**What we'd revisit**: The constants came from literature, not our data. With ground truth, we'd re-tune.

---

## 7. Cold Tier: Opt-in Profile, SeaweedFS

**Requirement**: Cold tier must work without external deps. MinIO images vanished from Docker Hub.

**Decision**: `docker compose --profile cold up` with `chrislusf/seaweedfs:4.48`

**Why**: SeaweedFS `weed mini` creates S3-compatible API + bucket at startup. No MinIO, no credentials management, no external network. `S3_ENDPOINT` empty = cold tier disabled (default).

**What we'd revisit**: SeaweedFS is less battle-tested than MinIO. For production, swap to MinIO or real S3.

---

## 8. API Design: Explicit Over Magic

**Requirement**: `/api/variants/:name` originally only accepted bare variant names. Users copying `ewma:500` from the UI got 404.

**Decision**: Accept both `ewma` and `ewma:100`; return resolved `variant` + `combination`.

**Why**: UI dropdowns show full combo names; users expect to copy-paste. 404 on `ewma:500` was a usability trap. Distinct errors: "Unknown variant" vs "Invalid combination".

---

## 9. Service IDs with Slashes

**Requirement**: Operation names like "GET /" produce service IDs with literal `/`. Gin decoded `%2F` → path split → 301 redirect → 404.

**Decision**: `router.UseRawPath = true; router.UnescapePathValues = true`

**Why**: Keeps `%2F` intact through routing. `c.Param("variant")` returns the still-escaped value. No manual encoding/decoding in handlers.

---

## 10. What We Didn't Do (And Why)

| Idea | Why Not |
|------|---------|
| ML-based anomaly detection | No labeled data; heuristic is transparent and debuggable |
| Per-service `LEARN_COMBINATIONS` | Adds config surface; global is simpler for demo |
| Trace sampling at ingest | Already sampling at baseline (reservoir 1000); double-sampling loses signal |
| Multi-tenant API | Single-tenant demo; auth adds noise not signal |
| WebSocket for live UI | Polling (30s) is sufficient for demo latency; SSE is overkill |
| Schema migrations | Demo scope; `CLEAN_START=true` is sufficient |
