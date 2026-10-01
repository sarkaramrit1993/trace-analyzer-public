# Known Limitations (Honest)

These are not bugs — they're design boundaries. If you're evaluating this for production use, read this first.

---

## Detection Accuracy

- **No ground-truth evaluation**. The deviation/anomaly scores are heuristic. We don't know precision/recall on real incidents.
- **Fingerprint granularity is coarse on simple apps**. On HotROD (712 traces), WL hash produces ~5 fingerprints. A plain span count separates traces just as well. Real services with input-dependent fan-out (loops, batch sizes) produce many more — but also fragment baselines.
- **`same_time_yesterday` compares only hourly history**. It doesn't model weekly seasonality, holidays, or gradual trend shifts. Weekend traffic is compared against Friday.
- **Frequency-shift detection rarely fires**. The 5-minute window needs sustained volume; bursty traffic looks like noise.
- **Anomaly score flags fast traces as "slow"**. Absolute value treats both tails the same. A skipped step (fast) scores same as a slow path.

---

## Scalability & Performance

| Dimension | Current Limit | Why |
|-----------|---------------|-----|
| Unique paths per service | ~2000 before memory pressure | 20 combos × 1000 reservoir samples × path metadata |
| Trace throughput | ~10k spans/sec (single process) | SSE ingest + assembler + 20× baseline update are serial per trace |
| Retention | 24h hot (SQLite), indefinite cold (S3) | No automated cold-tier expiration; `ColdRetentionHours` config ignored |
| Concurrent writers | 1 (SQLite) | Not designed for HA |

**Not benchmarked under sustained load**. The 17 µs/trace figure is from a synthetic benchmark on a dev laptop.

---

## Operational Gaps

- **No multi-tenancy**. Single database, single namespace.
- **No authentication/authorization**. Dev defaults (`tracedev`/`tracedevsecret`) only.
- **No metrics export (Prometheus/OpenTelemetry)**. `/api/stats` is the only observability.
- **No alerting**. Deviation/anomaly rows sit in SQLite; no webhook, no PagerDuty.
- **No schema migrations**. SQLite schema changes require `CLEAN_START=true`.
- **Cold tier bucket not created by analyzer**. Compose `s3` service does it; external S3 needs manual bucket + policy.
- **SSE reconnection is naive**. 5s fixed backoff; no exponential backoff, no dead-letter queue.

---

## Data Model Limits

- **Operation name = fingerprint differentiator**. Raw URLs with IDs (e.g., `/order/12345`) fragment fingerprints. Need tag-based normalization upstream.
- **Async work is invisible**. Queue handoffs show as separate traces. No cross-trace correlation.
- **Broken traces = extra roots**. Late/dropped spans become separate traces with same `TraceID`, different fingerprint.
- **Depth >20 not distinguished**. WL iterations = 20; deeper differences only visible through ancestor nodes.

---

## What Would Change for Production

1. **Add ground-truth evaluation** — replay labeled incidents, measure precision/recall per deviation type
2. **Replace WL fingerprint with configurable extractor** — allow "collapse repeated children" and "normalize operation names"
3. **Add Prometheus exporter** — expose `/metrics` with per-combo counters
4. **Implement cold-tier expiration** — respect `ColdRetentionHours` with lifecycle policy
5. **Add schema versioning + migrations** — `goose` or `sql-migrate`
6. **HA ingest** — stateless SSE receivers writing to Kafka, analysis workers consume
7. **Authentication + RBAC** — at minimum, API keys per tenant
8. **Distributed tracing integration** — export fingerprints as span attributes to Tempo/Jaeger

---

## What We're Proud Of (Despite Above)

- **Zero external dependencies** for the full stack (Docker + Go + React only)
- **Honest about limits** — every limitation above has a ticket in the private repo
- **Restart survives with fidelity** — all 20 combos + hourly history persist
- **Trace-time semantics** — replay and backfill work correctly
- **Test-first throughout** — 7 Go packages + 657 UI tests + 77 Python pipeline tests, all `-race` clean
