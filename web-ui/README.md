# Trace Analyzer Web UI

React dashboard for the analyzer: services, topology distribution, deviations, anomalies and trace detail. See the [root README](../README.md) for what the analyzer does.

## Commands

Needs Node 20.19 or newer.

```bash
npm ci
npm run dev        # Vite dev server on port 3000
npm run typecheck  # tsc --noEmit
npm test           # vitest run
npm run build      # production build into dist/
```

## How it's put together

`src/App.tsx` holds the app state in React `useState`. Requests go through the small `getJSON` and `postJSON` helpers in `src/api.ts`, which throw on non-2xx responses. Background loads run through the `usePolling` hook: it fetches once, then repeats while auto-refresh is on (service list every 5s, stats every 2s, the selected service's data every 3s). When its inputs change, say you pick another service, it aborts the request in flight so a slow response for the old service can't overwrite the new one. It also never starts a request while the previous one is still running.

The selected service is mirrored to `?service_id=` in the URL, so a reload or a shared link opens the same service. It uses `history.replaceState`, so switching services doesn't add back-button entries, and other query params and the hash are left alone. On load the URL beats the service saved in localStorage; if the URL names a service that doesn't exist, the app falls back and rewrites the URL to match. The URL stays untouched until the service list has loaded. Only the service goes in the URL. The open trace view does not.

The scope, env and service identity filters go to `/api/services` as query params and the analyzer filters on them. The browser applies the same filters again, plus the search box and the errors, deviations and anomalies checkboxes. Switching the baseline combination refetches services with the same query as the poll.

```
src/
├── App.tsx                 # State, polling, layout
├── api.ts                  # API_URL resolution, getJSON and postJSON helpers
├── main.tsx                # Entry point
├── hooks/
│   └── usePolling.ts       # Poll an async fn, abort stale requests, no overlap
├── components/
│   ├── common/             # Card, Stat, MetricCard, HelpIcon
│   ├── layout/             # Header (stats, variant selector), Sidebar (service list, filters)
│   ├── topology/           # TopologyModal wrapping SimpleGraph, an SVG topology graph
│   └── traces/             # TraceDetailView, with TraceWaterfall and TimeAxis for the timeline
├── types/                  # Types matching the Go API's JSON
└── utils/                  # criticalPath, graphData, spanValidation, traceUtils
```

## API URL

The UI picks the analyzer URL in this order:

1. `window.APP_CONFIG.API_URL`, from `/config.js`. In Docker, `entrypoint.sh` writes that file from `VITE_API_URL` when the container starts.
2. `import.meta.env.VITE_API_URL` at build time.
3. `http://localhost:8080`.

## Endpoints used

`/api/services`, `/api/services/:serviceId`, `/api/stats`, `/api/topologies`, `/api/deviations`, `/api/anomalies`, `/api/traces/recent`, `/api/traces/by-fingerprint`, `/api/traces/:traceId`, `/api/variants/combinations` and `POST /api/variants/combination`. The full route list is in the root README.
