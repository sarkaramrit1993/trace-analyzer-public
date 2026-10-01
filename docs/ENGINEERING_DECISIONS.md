# Engineering Decisions (Staff Engineer Log)

Each entry: **Context → Options → Decision → Why → What we'd revisit**

---

## 1. Baseline Strategy: 20 Parallel Combinations

**Context**: Different failure modes need different statistical approaches. EWMA catches sudden shifts; cumulative catches slow drift; sliding window catches seasonal patterns.

**Options**:
- A) One adaptive baseline that switches strategies
- B) Fixed set of parallel baselines (what we did)
- C) User-configurable pipeline per service

**Decision**: B — fixed 20 combinations (5 variants × 4 thresholds)

**Why**: 
- No strategy reliably dominates; parallel is safer than adaptive
- 20 is small enough to run per-trace (17 µs on M1)
- `LEARN_COMBINATIONS` lets operators narrow it later

**Revisit**: Under real load, measure if EWMA+sliding_window suffice and drop the others.

---

## 2. Fingerprinting: Weisfeiler-Lehman (20 iterations)

**Context**: Need a stable ID for "same execution path" that survives span reordering and minor topology changes.

**Options**:
- A) Span sequence hash (order-sensitive)
- B) WL graph hash (structure-aware, 20 iterations)
- C) Path signature (simplified span tree)

**Decision**: B — WL with 20 iterations

**Why**:
- Structure matters more than exact span order in distributed traces
- 20 iterations covers depth of any real service call graph
- 64-bit output: collisions negligible at <1000 paths/service

**Revisit**: If fan-out varies per request (loops, batch sizes), WL fragments baselines. Could add a "collapse repeated children" pre-pass.

---

## 3. Trace Time vs Wall Clock

**Context**: Initial implementation used `time.Now()` for new-path windows, decay, and cleanup. During fast ingest or replay, every path looked "new" for 10 real minutes.

**Options**:
- A) Keep wall clock, add ingestion-rate awareness
- B) Switch entirely to trace `StartTime` (what we did)
- C) Hybrid: trace time for decay, wall clock for retention

**Decision**: B — pure trace time

**Why**:
- Replay and backfill are first-class use cases
- New-path window, decay age, and hourly cleanup all compare like-with-like
- `MaxFutureSkew = 5 min` prevents one bad timestamp from corrupting history

**Revisit**: If traces arrive out of order by >5 min, the skew clamp drops data. Could add a reorder buffer.

---

## 4. Baseline Persistence: Per-Combination, Not Just Active

**Context**: Original code saved only the active combo. Restart collapsed 20 combos → 1. Switching combos after restart gave empty baselines.

**Options**:
- A) Save only active (less I/O)
- B) Save all learned combos, each under its own key (what we did)
- C) Save active + snapshot others periodically

**Decision**: B — every combo gets its own row

**Why**:
- Restart fidelity matters more than I/O cost (400 rows = ~0.4 s)
- Legacy row upgrade path: old DBs seed all 20 from the single saved row
- Failed save rolls back; previous save stays intact

**Revisit**: If combo count grows, add "dirty flag" to skip unchanged combos.

---

## 5. Deviation Types: Three Independent Checks

**Context**: Single deviation score conflates different signals.

**Decision**: Three independent checks, each produces its own row:

| Type | Signal | Threshold |
|------|--------|-----------|
| `new_path` | First time seeing this fingerprint | Always fires once per combo |
| `rare_path` | `freq < 1/(TotalTraces×100)` | Requires min-samples |
| `freq_shift` | `|freq_t - freq_{t-5m}| > 2×σ` | 5-min rolling window |

**Why**: Different remediation actions. New path = new code path. Rare path = config drift. Freq shift = load pattern change.

**Revisit**: `freq_shift` rarely fires on low-volume services. Could add a volume gate.

---

## 6. Anomaly Scoring: Weighted Z-Score (Modified)

**Context**: Standard z-score assumes normal distribution; latencies are heavy-tailed.

**Decision**: `|0.6745 × (x - P75) / (IQR / 1.35)|` with cutoff 3.5

**Why**:
- MAD-based modified z-score is robust to outliers
- 0.6745 converts MAD to σ for normal; 1.35 converts IQR to σ
- Using both gives ~0.67× plain IQR z-score — empirically tuned

**Revisit**: The constants came from literature, not our data. With ground truth, we'd re-tune.

---

## 7. Cold Tier: Opt-in Profile, SeaweedFS

**Context**: MinIO images vanished from Docker Hub; cold tier must work without external deps.

**Decision**: `docker compose --profile cold up` with `chrislusf/seaweedfs:4.48`

**Why**:
- SeaweedFS `weed mini` creates S3-compatible API + bucket at startup
- No MinIO, no credentials management, no external network
- `S3_ENDPOINT` empty = cold tier disabled (default)

**Revisit**: SeaweedFS is less battle-tested than MinIO. For production, swap to MinIO or real S3.

---

## 8. API Design: Explicit Over Magic

**Context**: `/api/variants/:name` originally only accepted bare variant names.

**Decision**: Accept both `ewma` and `ewma:100`; return resolved `variant` + `combination`.

**Why**:
- UI dropdowns show full combo names; users expect to copy-paste
- 404 on `ewma:500` was a usability trap
- Distinct errors: "Unknown variant" vs "Invalid combination"

---

## 9. Service IDs with Slashes

**Context**: Operation names like "GET /" produce service IDs with literal `/`. Gin decoded `%2F` → path split → 301 redirect → 404.

**Decision**: `router.UseRawPath = true; router.UnescapePathValues = true`

**Why**:
- Keeps `%2F` intact through routing
- `c.Param("variant")` returns the still-escaped value
- No manual encoding/decoding in handlers

---

## 10. What We Didn't Do (And Why)

| Idea | Why Not |
|------|---------|
| ML-based anomaly detection | No labeled data; heuristic is transparent and debuggable |
| Per-service LEARN_COMBINATIONS | Adds config surface; global is simpler for demo |
| Trace sampling at ingest | Already sampling at baseline (reservoir 1000); double-sampling loses signal |
| Multi-tenant API | Single-tenant demo; auth adds noise not signal |
| WebSocket for live UI | Polling (30s) is sufficient for demo latency; SSE is overkill |
