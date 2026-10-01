# Runbook

## Running

Default stack, analyzer plus web UI, hot and warm tiers in SQLite:

```bash
docker compose up --build
```

With the cold tier on SeaweedFS:

```bash
S3_ENDPOINT=http://s3:8333 docker compose --profile cold up --build
```

The `cold` profile adds one service, `s3`, running `chrislusf/seaweedfs:4.48` as `weed mini -dir=/data -bucket=trace-cold`. That single process runs the SeaweedFS master, volume server, filer and S3 gateway, and creates the bucket if it doesn't exist. Credentials come from `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` on the service, which SeaweedFS turns into an admin identity. Unsigned requests get a 403.

The analyzer doesn't depend on `s3` in compose, so the default stack stays free of it. Setting up the S3 client doesn't contact the endpoint either. If `S3_ENDPOINT` is set but the server isn't reachable, the analyzer still starts and the problem shows up later as `Failed to write to cold tier` and cold compaction errors in the logs.

What gets written and when:

- Error, deviation and anomaly traces go to `interesting-traces/YYYY/MM/DD/HH/interesting_<ns>.json.gz` right after the trace is flagged, one object per trace. The hour in the key comes from the trace's start time.
- Rollups go to `rollups/YYYY/MM/DD/HH/rollups_<ns>.json.gz`. The rollup loop flushes finished 15-minute windows to SQLite every 15 minutes, and the cold compaction loop moves rollups older than `WARM_RETENTION_MINUTES` to S3 once an hour. Both intervals are hardcoded, so even with `WARM_RETENTION_MINUTES=1` the first rollup object shows up about an hour after startup.

To look inside the bucket, run the AWS CLI on the compose network:

```bash
docker run --rm --network trace-analyzer_default \
  -e AWS_ACCESS_KEY_ID=tracedev -e AWS_SECRET_ACCESS_KEY=tracedevsecret -e AWS_DEFAULT_REGION=us-east-1 \
  amazon/aws-cli:2.37.7 --endpoint-url http://s3:8333 s3 ls s3://trace-cold --recursive
```

The network is named after the checkout directory (`<dir>_default`); `docker network ls` shows it.

Without Docker, see "Local development" in the [README](../README.md).

There's no separate lite compose file. The default stack without the `cold` profile is the lightweight option.

## Ports

| Service | Host port | Container port | Source |
|---------|-----------|----------------|--------|
| analyzer | 18080 | 8080 (`API_PORT`) | `docker-compose.yml` |
| web-ui | 14000 | 4000 (`serve -l 4000`) | `docker-compose.yml`, `web-ui/Dockerfile` |
| s3 (SeaweedFS S3 API, `cold` profile) | 19000 | 8333 | `docker-compose.yml` |
| analyzer, `go run` | 8080 | n/a | default `API_PORT` in `analyzer/internal/models/models.go` |
| web-ui, `npm run dev` | 3000 | n/a | `web-ui/vite.config.js` |

## Environment variables

Analyzer settings are read in `loadConfig` in `analyzer/cmd/analyzer/main.go`, except `HOURLY_PATH_SAMPLE_CAP`, which `hourlyPathSampleCap` in the same file reads. Defaults come from `DefaultConfig` in `analyzer/internal/models/models.go`. `CORS_ALLOWED_ORIGIN` is read in `analyzer/internal/api/server.go`.

| Variable | Default | Compose value | What it does |
|----------|---------|---------------|--------------|
| `SSE_ENDPOINT` | `disabled` | `disabled` | URL of an SSE span stream. Empty or `disabled` turns the client off. |
| `API_PORT` | `8080` | `8080` | HTTP listen port. |
| `DATA_PATH` | `./data` | `/app/data` | Directory for `traces.db`. Created if missing. The analyzer Dockerfile also sets `/app/data`. |
| `CLEAN_START` | `false` | `false` | When true, deletes `traces.db`, `traces.db-wal` and `traces.db-shm` at startup, so every combination's persisted baselines, the `same_time_yesterday` hourly history and the active combination saved from the API start empty. |
| `HOT_RETENTION_MINUTES` | `15` | `15` | Age after which plain traces are deleted. Important traces and deviation/anomaly rows are kept 10x longer. |
| `WARM_RETENTION_MINUTES` | `60` | `60` | Age after which rollups move to the cold tier. Only applies when a cold tier is configured. |
| `TRACE_ASSEMBLY_TIMEOUT_SECS` | `180` | `180` | Seconds from a trace's first span until it's emitted for analysis. Accepts decimals. |
| `DEVIATION_THRESHOLD` | `0.3` | `0.1` | Frequency below which a path counts as rare for spike and rare-path checks. Not a score cutoff. Values of 0 or less become 0.01. |
| `ANOMALY_ZSCORE_THRESHOLD` | `3.5` | `3.5` | Minimum anomaly score to record an anomaly. |
| `ANOMALY_BASELINE_PERCENTILE` | `0.75` | not set | Reference percentile for anomaly scoring. Values outside (0, 1) become 0.75. |
| `LEARN_COMBINATIONS` | `all` | not set | Which baseline combinations learn. `all` (or unset) is all 20, `active` is only `ACTIVE_COMBINATION`, otherwise a comma-separated list like `cumulative:50,ewma:500`. An invalid value stops the analyzer at startup and logs the valid names. Combinations not learned are neither checked nor listed by the API. |
| `ACTIVE_COMBINATION` | `cumulative:50` | not set | Combination active at startup, whose findings the API and UI show. When set, it overrides the choice saved from the API. Must be learned; an unknown or not learned name stops startup with the list of valid ones. When unset, the last API choice is used if still learned, else `cumulative:50`. |
| `SLIDING_WINDOW_DURATION` | `24h` | `24h` | Window for the `sliding_window` variant, Go duration syntax. Invalid values fall back to 24h. |
| `HOURLY_PATH_SAMPLE_CAP` | `200` | not set | Duration samples kept per hour, service and path for `same_time_yesterday` anomaly scoring. Bounds that variant's memory at `cap x 8` bytes per (hour, service, path). A non-positive or non-numeric value logs a warning and uses 200. |
| `SERVICE_IDENTITY_FIELDS` | `service_identity,scope1,scope2,scope3,env,feature_group,feature_name,sub_service,operation` | not set | Comma-separated fields that make up the service ID. Also accepts `service_name`, `operation_name` or any raw root-span tag key. |
| `SERVICE_IDENTITY_TAG_KEYS` | `service_identity,serviceIdentity,serviceName,localServiceName,service_name,istio.canonical_service` | `${SERVICE_IDENTITY_TAG_KEYS:-}` (empty unless set in your shell) | Comma-separated span tag keys checked in order for the service identity value. Empty keeps the default. Falls back to the span's `ServiceName` when no key has a value. |
| `SCOPE1_TAG_KEYS` | `scope1,scope_1,scope-1` | `${SCOPE1_TAG_KEYS:-}` | Comma-separated span tag keys checked in order for scope1. Empty keeps the default. |
| `SCOPE2_TAG_KEYS` | `scope2,scope_2,scope-2` | `${SCOPE2_TAG_KEYS:-}` | Same, for scope2. |
| `SCOPE3_TAG_KEYS` | `scope3,scope_3,scope-3` | `${SCOPE3_TAG_KEYS:-}` | Same, for scope3. |
| `S3_ENDPOINT` | empty | `${S3_ENDPOINT:-}` (empty unless set in your shell) | Cold tier endpoint including scheme. Empty disables the cold tier. |
| `S3_ACCESS_KEY` | empty | `tracedev` | Cold tier access key. |
| `S3_SECRET_KEY` | empty | `tracedevsecret` | Cold tier secret key. |
| `S3_BUCKET` | `trace-cold` | `trace-cold` | Cold tier bucket. The analyzer doesn't create it; the `s3` service does in the `cold` profile. |
| `S3_USE_SSL` | `false` | `false` | Parsed but not used. HTTP vs HTTPS comes from the scheme in `S3_ENDPOINT`. |
| `CORS_ALLOWED_ORIGIN` | unset | not set | Comma-separated list of allowed origins, which replaces the defaults, or `*` (any origin, never with credentials). Unset allows only http://localhost:14000, http://127.0.0.1:14000, http://localhost:3000 and http://127.0.0.1:3000. Also controls error detail: when set to a value without `localhost`, API errors are replaced by a generic message. |

The S3 region is hardcoded to `us-east-1` and path-style addressing is always on (`analyzer/internal/storage/cold_s3.go`).

Web UI:

| Variable | Default | What it does |
|----------|---------|--------------|
| `VITE_API_URL` | `http://localhost:8080` | In the container, `web-ui/entrypoint.sh` writes it into `dist/config.js` as `window.APP_CONFIG.API_URL` at startup. Compose sets `http://localhost:18080`. In `npm run dev`, `web-ui/public/config.js` already defines `APP_CONFIG.API_URL`, which takes priority over `VITE_API_URL`. |

**Upgrading with prefixed scope tags.** Older builds also read one vendor-prefixed key for each scope. If your scope tags use a prefixed key (for example `team.scope1`), set `SCOPE1_TAG_KEYS`, `SCOPE2_TAG_KEYS` and `SCOPE3_TAG_KEYS` to include it, e.g. `SCOPE1_TAG_KEYS=team.scope1,scope1`. Otherwise those values stop being read, which changes service IDs and fingerprints, so start with `CLEAN_START=true` to avoid mixing old and new baselines. Only the values feed fingerprints, so mapping the old key back keeps existing baselines valid.

## Health checks

| Check | Command | Expected |
|-------|---------|----------|
| Analyzer liveness | `curl -s http://localhost:18080/health` | `{"status":"healthy"}` |
| Analyzer counters | `curl -s http://localhost:18080/api/stats` | JSON with trace, deviation and anomaly counts and `uptime_seconds` |
| Active baseline | `curl -s http://localhost:18080/api/variants/active` | JSON with `combination`, e.g. `cumulative:50` |
| Web UI | `curl -s http://localhost:14000/config.js` | A script setting `window.APP_CONFIG` with the API URL |
| Compose view | `docker compose ps` | analyzer and web-ui `healthy` |

Compose healthchecks: the analyzer runs `wget` against `/health` every 10 seconds, the web UI runs `wget` against `/config.js`, and the web UI waits for the analyzer to be healthy before it starts. The `s3` service (profile `cold`) runs `wget` against the filer's `/buckets/trace-cold/` listing, so it only turns healthy once the bucket exists.

`/health` only says the HTTP server is up. It doesn't check SQLite, the SSE connection or the cold tier. For those, read the analyzer logs: every 10 seconds it logs a `Stats` line with `sse_spans`, `sse_errors`, `spans_processed`, `traces_emitted`, `pending_traces`, `stored_traces`, `deviations`, `anomalies`, `pending_rollups`, `cold_objects`, `cold_bytes`, `hourly_buckets` and `hourly_samples`. `cold_objects` and `cold_bytes` count objects under both `rollups/` and `interesting-traces/`. `hourly_buckets` and `hourly_samples` are the (hour, service) buckets and stored duration samples in the `same_time_yesterday` history, both 0 when no `same_time_yesterday` combination is learned.

```bash
docker compose logs -f analyzer
```

## Resetting state

All analyzer state lives in `traces.db` under `DATA_PATH` (compose volume `trace-data`) plus in-memory baselines. Baselines for every learned combination and the `same_time_yesterday` hourly history are saved to `traces.db` every 5 minutes and on shutdown, so a restart loses at most 5 minutes of learning after a crash and nothing after a clean stop.

The shutdown save of all 20 combinations takes well under a second for typical loads, but compose gives the analyzer `stop_grace_period: 30s` so a slow disk or a big pending-trace flush isn't cut off by the default 10 seconds. If you run it elsewhere, give it a similar grace period before SIGKILL.

Wipe the SQLite database but keep the volume: compose hardcodes `CLEAN_START: "false"`, so change it to `"true"` in `docker-compose.yml`, run `docker compose up -d analyzer`, then set it back. If you leave it on, every restart starts from an empty database. Locally, run `CLEAN_START=true go run ./cmd/analyzer` from `analyzer/`, or stop the process and delete `analyzer/data/`.

Wipe everything, including the S3 data if you used the `cold` profile:

```bash
docker compose --profile cold down -v
```

`down -v` removes the containers and the `trace-data` and `s3-data` volumes. Passing `--profile cold` makes sure the `s3` service is included in the teardown.

Reset only the baseline choice without losing data:

```bash
curl -X POST http://localhost:18080/api/variants/combination \
  -H 'Content-Type: application/json' -d '{"combination":"cumulative:50"}'
```

A switch made through the API (either `POST /api/variants/combination` or `POST /api/variants/active`) is saved in the `settings` table of `traces.db` as soon as it's made, and a restart keeps it. On startup the active combination is picked in this order:

1. `ACTIVE_COMBINATION`, if it's set to a non-empty value. It must be learned, or startup stops with the valid names.
2. The combination last chosen through the API, if it's still learned. If `LEARN_COMBINATIONS` has since dropped it, the analyzer logs a warning naming it and moves on.
3. `cumulative:50`, if learned.
4. The first learned combination.

So if `ACTIVE_COMBINATION` is set (compose or a `.env`), it wins on every restart and an API switch lasts only until the next one. Unset it to let API choices stick. `CLEAN_START=true` deletes the database, so the saved choice goes with it. If the save itself fails, the switch still applies and an error is logged saying the restart won't keep it. Each combination's baselines are persisted either way.

## Common problems

**Nothing shows up after sending spans.** Traces are only analyzed `TRACE_ASSEMBLY_TIMEOUT_SECS` after their first span, 180 seconds by default. Check `pending_traces` and `traces_emitted` in the `Stats` log line.

**Services appear but no deviations or anomalies.** Detection waits for min-samples traces per service and per path. With the default `cumulative:50`, a path needs 50 traces before it's scored for anomalies. Also see [RESEARCH_QUESTIONS.md](RESEARCH_QUESTIONS.md) for why some deviation types rarely fire.

**Switched combination and the counts reset.** Deviation and anomaly counts in `/api/stats` are filtered by the active combination, and non-active combinations don't learn from live traffic, so a freshly selected combination may have an empty baseline.

**Service IDs look like `frontend::::::::GET /`.** That's the default `SERVICE_IDENTITY_FIELDS` with most tags missing. Set `SERVICE_IDENTITY_FIELDS=service_name,operation_name` for plain Jaeger data. This changes service IDs, so start with `CLEAN_START=true`.
