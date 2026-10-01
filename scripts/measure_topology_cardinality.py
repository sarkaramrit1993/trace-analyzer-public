#!/usr/bin/env python3
"""Measure topology cardinality: how many traces collapse into how many fingerprints.

Pulls traces from Jaeger's query API (/api/v3/services and /api/v3/traces,
OTLP JSON; Jaeger v2.21 removed the old /api/traces endpoints), replays them
into a trace analyzer via POST /api/spans/batch, waits for assembly, then prints per-service trace and
fingerprint counts from the analyzer's API.

The analyzer MUST be fresh (no traces stored, e.g. CLEAN_START=true and a new
DATA_PATH). Replaying into a non-empty analyzer would double count, so the
script refuses to run if /api/stats reports any stored traces.

It also computes, from the Jaeger data alone, three independent sanity checks
per root (service, operation):
  shapes      distinct canonical call trees (service|operation labels, children
              sorted); should equal the analyzer's fingerprint count
  mixes       distinct multisets of (service, operation) span counts; if this is
              lower than shapes, structure matters beyond "which spans, how many"
  sizes       distinct total span counts per trace

Python 3 stdlib only.

  python3 scripts/measure_topology_cardinality.py \\
      --jaeger-url http://localhost:16686 --analyzer-url http://localhost:8080
"""
import argparse
import json
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter, defaultdict

BATCH = 1000


def get_json(url):
    with urllib.request.urlopen(url, timeout=60) as r:
        return json.load(r)


def post_json(url, body):
    req = urllib.request.Request(url, data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.load(r)


def attr_map(attrs):
    out = {}
    for a in attrs or []:
        v = a.get("value", {})
        out[a["key"]] = str(next(iter(v.values()), "")) if v else ""
    return out


def convert(span, service):
    tags = attr_map(span.get("attributes"))
    if span.get("status", {}).get("code") in (2, "STATUS_CODE_ERROR"):
        tags["error"] = "true"
    start = int(span["startTimeUnixNano"])
    return {
        "TraceID": span["traceId"],
        "SpanID": span["spanId"],
        "ParentID": span.get("parentSpanId", ""),
        "OperationName": span["name"],
        "StartTime": start // 1000,
        "Duration": (int(span["endTimeUnixNano"]) - start) // 1000,
        "ServiceName": service,
        "Tags": tags,
    }


def fetch_spans(jaeger, limit, lookback_secs, skip):
    services = [s for s in get_json(f"{jaeger}/api/v3/services").get("services") or [] if s not in skip]
    now = time.time()
    fmt = lambda t: time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(t))
    spans = {}
    for svc in services:
        q = urllib.parse.urlencode({
            "query.service_name": svc, "query.search_depth": limit,
            "query.start_time_min": fmt(now - lookback_secs), "query.start_time_max": fmt(now + 60),
        })
        try:
            result = get_json(f"{jaeger}/api/v3/traces?{q}").get("result") or {}
        except urllib.error.HTTPError as e:
            if e.code == 404:
                continue
            raise
        for rs in result.get("resourceSpans") or []:
            service = attr_map(rs.get("resource", {}).get("attributes")).get("service.name", "unknown")
            for ss in rs.get("scopeSpans") or []:
                for s in ss.get("spans") or []:
                    spans[(s["traceId"], s["spanId"])] = convert(s, service)
        print(f"jaeger: service={svc} cumulative_spans={len(spans)}", file=sys.stderr)
    return list(spans.values())


def canonical(span_id, by_id, children):
    s = by_id[span_id]
    kids = sorted(canonical(c, by_id, children) for c in children.get(span_id, []))
    return f"{s['ServiceName']}|{s['OperationName']}(" + ",".join(kids) + ")"


def jaeger_side_stats(spans):
    traces = defaultdict(list)
    for s in spans:
        traces[s["TraceID"]].append(s)
    per_root = defaultdict(lambda: {"traces": 0, "shapes": set(), "mixes": set(), "sizes": set()})
    bad_roots = 0
    for tid, ss in traces.items():
        by_id = {s["SpanID"]: s for s in ss}
        children = defaultdict(list)
        roots = []
        for s in ss:
            if s["ParentID"] and s["ParentID"] in by_id:
                children[s["ParentID"]].append(s["SpanID"])
            else:
                roots.append(s)
        if len(roots) != 1 or roots[0]["ParentID"]:
            bad_roots += 1
        root = min(roots, key=lambda s: s["StartTime"])
        key = f"{root['ServiceName']}:{root['OperationName']}"
        mix = frozenset(Counter((s["ServiceName"], s["OperationName"]) for s in ss).items())
        shape = ",".join(sorted(canonical(r["SpanID"], by_id, children) for r in roots))
        e = per_root[key]
        e["traces"] += 1
        e["shapes"].add(shape)
        e["mixes"].add(mix)
        e["sizes"].add(len(ss))
    return traces, per_root, bad_roots


def replay(analyzer, spans):
    spans = sorted(spans, key=lambda s: (s["TraceID"], s["StartTime"]))
    for i in range(0, len(spans), BATCH):
        resp = post_json(f"{analyzer}/api/spans/batch", spans[i:i + BATCH])
        if resp.get("rejected"):
            print(f"warning: batch {i // BATCH} rejected {resp['rejected']} spans", file=sys.stderr)


def wait_for_traces(analyzer, expected, timeout):
    deadline = time.time() + timeout
    last = -1
    while time.time() < deadline:
        n = get_json(f"{analyzer}/api/stats").get("hot_tier_traces", 0)
        if n >= expected:
            return n
        if n != last:
            print(f"analyzer: {n}/{expected} traces assembled", file=sys.stderr)
            last = n
        time.sleep(2)
    return get_json(f"{analyzer}/api/stats").get("hot_tier_traces", 0)


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--jaeger-url", default="http://localhost:16686")
    ap.add_argument("--analyzer-url", default="http://localhost:8080")
    ap.add_argument("--limit", type=int, default=2000, help="max traces per Jaeger service query (query.search_depth)")
    ap.add_argument("--lookback-secs", type=int, default=7200)
    ap.add_argument("--skip-services", default="jaeger",
                    help="comma separated services not to query (Jaeger traces itself)")
    ap.add_argument("--wait-secs", type=int, default=120)
    args = ap.parse_args()
    jaeger, analyzer = args.jaeger_url.rstrip("/"), args.analyzer_url.rstrip("/")

    if get_json(f"{analyzer}/api/stats").get("hot_tier_traces", 0):
        sys.exit("analyzer already holds traces; restart it fresh (CLEAN_START=true) to avoid double counting")

    spans = fetch_spans(jaeger, args.limit, args.lookback_secs, set(args.skip_services.split(",")))
    traces, per_root, bad_roots = jaeger_side_stats(spans)
    print(f"sent: {len(spans)} spans in {len(traces)} traces "
          f"({bad_roots} traces without exactly one root span)")

    t0 = time.time()
    replay(analyzer, spans)
    got = wait_for_traces(analyzer, len(traces), args.wait_secs)
    print(f"analyzer stored {got} traces ({time.time() - t0:.1f}s incl. assembly timeout)")

    services = sorted(get_json(f"{analyzer}/api/services"), key=lambda s: -s["trace_count"])
    hdr = f"{'service_id':<42} {'traces':>6} {'fprints':>7} {'shapes':>6} {'mixes':>5} {'sizes':>5}  top-3 fingerprint shares"
    print("\n" + hdr + "\n" + "-" * len(hdr))
    tot_t = tot_f = 0
    for svc in services:
        sid = svc["service_id"]
        q = urllib.parse.urlencode({"service_id": sid})
        tops = sorted(get_json(f"{analyzer}/api/topologies?{q}"), key=lambda t: -t["count"])
        shares = ", ".join(f"{t['percentage']:.1f}%" for t in tops[:3])
        j = per_root.get(sid, {"shapes": (), "mixes": (), "sizes": ()})
        print(f"{sid:<42} {svc['trace_count']:>6} {len(tops):>7} {len(j['shapes']):>6} "
              f"{len(j['mixes']):>5} {len(j['sizes']):>5}  {shares}")
        tot_t += svc["trace_count"]
        tot_f += len(tops)
    print("-" * len(hdr))
    print(f"{'TOTAL (' + str(len(services)) + ' root service:operation groups)':<42} {tot_t:>6} {tot_f:>7} "
          f"{sum(len(e['shapes']) for e in per_root.values()):>6} "
          f"{sum(len(e['mixes']) for e in per_root.values()):>5}")
    print(f"\ntraces per fingerprint: {tot_t / max(tot_f, 1):.1f}  "
          f"(fingerprints / traces = {tot_f / max(tot_t, 1):.3f})")
    if tot_t != len(traces):
        print(f"WARNING: analyzer counted {tot_t} traces but {len(traces)} were sent")


if __name__ == "__main__":
    main()
