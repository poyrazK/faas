#!/usr/bin/env python3
"""Go/no-go check for observability on the control plane (ADR-20261023124955 §7).

Run on a control plane after the observability stack has run for the
measurement window (72 h by default). Reads only Prometheus, which holds the
per-slice numbers faas-cgroup-metrics writes, and prints one line per
check: PASS, FAIL or SKIP (no data to judge). Exit code 1 when any check
fails, 0 otherwise.

Relative checks (PostgreSQL CPU stall, platform wake p95) compare the window
with a baseline window of the same length that ends at --baseline-end, taken
before the stack was enabled. Without --baseline-end they are skipped.

    scripts/ops/obs_gate.py --window 72h --baseline-end 2026-10-20T00:00:00Z
"""

import argparse
import datetime as dt
import json
import re
import sys
import urllib.parse
import urllib.request

SHIPPER_JOBS = 'job=~"promtail|promtail-compute"'
UNITS = {"s": 1, "m": 60, "h": 3600, "d": 86400}
SIZE_UNITS = {"B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12}


def parse_duration(text):
    m = re.fullmatch(r"(\d+)([smhd])", text)
    if not m:
        raise argparse.ArgumentTypeError(f"duration like 72h expected, got {text!r}")
    return int(m.group(1)) * UNITS[m.group(2)]


def parse_size(text):
    m = re.fullmatch(r"(\d+)(B|KB|MB|GB|TB)", text)
    if not m:
        raise argparse.ArgumentTypeError(f"size like 40GB expected, got {text!r}")
    return int(m.group(1)) * SIZE_UNITS[m.group(2)]


def parse_time(text):
    if text == "now":
        return dt.datetime.now(dt.timezone.utc).timestamp()
    return dt.datetime.fromisoformat(text.replace("Z", "+00:00")).timestamp()


class Prometheus:
    def __init__(self, base, opener=None):
        self.base = base.rstrip("/")
        self.opener = opener or urllib.request.build_opener()

    def scalar(self, query, at):
        """The single value of an instant query, or None when it returns nothing."""
        url = f"{self.base}/api/v1/query?" + urllib.parse.urlencode({"query": query, "time": f"{at:.3f}"})
        with self.opener.open(url, timeout=60) as resp:
            body = json.load(resp)
        if body.get("status") != "success":
            raise RuntimeError(f"query failed: {query}: {body.get('error')}")
        result = body["data"]["result"]
        if not result:
            return None
        if len(result) > 1:
            raise RuntimeError(f"query returned {len(result)} series, want one: {query}")
        value = float(result[0]["value"][1])
        return None if value != value else value  # NaN means no data


def checks(window, retention_size, node_job="node", shipper="alloy"):
    """(name, query, comparator, threshold, unit, relative, required).

    Per-slice numbers are pinned to the control plane's node_exporter job:
    compute nodes run a faas-obs.slice too. A required check FAILs when its
    series is missing instead of being skipped.
    """
    w = f"{window}s"
    obs = f'job="{node_job}",cgroup="faas-obs.slice"'
    pg = f'job="{node_job}",cgroup="system.slice/system-postgresql.slice"'
    ship = f'job="{node_job}",cgroup="faas-obs.slice/{shipper}.service"'
    return [
        ("obs slice CPU, window average", f"avg_over_time(rate(faas_cgroup_cpu_usage_seconds_total{{{obs}}}[5m])[{w}:1m])", "<=", 0.25, "cores", False, False),
        ("obs slice CPU, worst 5 min", f"max_over_time(rate(faas_cgroup_cpu_usage_seconds_total{{{obs}}}[5m])[{w}:1m])", "<=", 0.60, "cores", False, False),
        ("obs slice anon memory peak / limit", f"max_over_time(faas_cgroup_memory_anon_bytes{{{obs}}}[{w}]) / faas_cgroup_memory_max_bytes{{{obs}}}", "<=", 0.80, "ratio", False, False),
        ("obs slice OOM kills", f'increase(faas_cgroup_memory_events_total{{{obs},event="oom_kill"}}[{w}])', "<=", 0, "kills", False, False),
        # ADR-20261023185953 Decision 5: the shipper's own budget on the control plane.
        (f"{shipper} peak anon memory", f"max_over_time(faas_cgroup_memory_anon_bytes{{{ship}}}[{w}])", "<=", 200e6, "bytes", False, False),
        (f"{shipper} CPU, window average", f"avg_over_time(rate(faas_cgroup_cpu_usage_seconds_total{{{ship}}}[5m])[{w}:1m])", "<=", 0.05, "cores", False, False),
        # The stalled-shipping alert needs a read counter. Alloy's name was not
        # confirmed when the rules were written, so its absence is a failure.
        ("journal read counter present", f"count(loki_source_journal_target_lines_total{{{SHIPPER_JOBS}}}) or count(promtail_journal_target_lines_total)", ">=", 1, "series", False, True),
        ("Prometheus 35 d disk projection", "max(prometheus_tsdb_storage_blocks_bytes) / ((time() - min(prometheus_tsdb_lowest_timestamp_seconds)) / 86400) * 35", "<=", retention_size, "bytes", False, False),
        ("journal shipper dropped entries", f"sum(increase(promtail_dropped_entries_total[{w}])) or sum(increase(loki_write_dropped_entries_total{{{SHIPPER_JOBS}}}[{w}]))", "<=", 0, "entries", False, False),
        ("PostgreSQL CPU stall vs baseline", f'avg_over_time(rate(faas_cgroup_pressure_stall_seconds_total{{{pg},resource="cpu",kind="some"}}[5m])[{w}:1m])', "delta<=", 0.05, "share", True, False),
        ("platform wake p95 vs baseline", f"histogram_quantile(0.95, sum by (le) (increase(gateway_platform_wake_latency_seconds_bucket[{w}])))", "ratio<=", 1.10, "s", True, False),
    ]


def evaluate(prom, end, window, baseline_end, retention_size, node_job="node", shipper="alloy"):
    rows, failed = [], False
    for name, query, op, limit, unit, relative, required in checks(window, retention_size, node_job, shipper):
        value = prom.scalar(query, end)
        base = prom.scalar(query, baseline_end) if relative and baseline_end is not None else None
        if value is None and required:
            rows.append(("FAIL", name, "no data; this series is required"))
            failed = True
            continue
        if value is None or (relative and base is None):
            reason = "no baseline" if relative and baseline_end is None else "no data"
            rows.append(("SKIP", name, reason))
            continue
        if op == "<=":
            ok, shown = value <= limit, f"{value:.4g} {unit} (limit {limit:.4g})"
        elif op == ">=":
            ok, shown = value >= limit, f"{value:.4g} {unit} (at least {limit:.4g})"
        elif op == "delta<=":
            ok, shown = value - base <= limit, f"{value:.4g} vs baseline {base:.4g} (allowed +{limit})"
        else:
            ok = base == 0 or value / base <= limit
            shown = f"{value:.4g} {unit} vs baseline {base:.4g} (allowed x{limit})"
        failed |= not ok
        rows.append(("PASS" if ok else "FAIL", name, shown))
    return rows, failed


def main(argv=None, prom=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--prometheus", default="http://127.0.0.1:9095")
    ap.add_argument("--end", default="now", help="RFC 3339 end of the measurement window, or now")
    ap.add_argument("--window", type=parse_duration, default=parse_duration("72h"))
    ap.add_argument("--baseline-end", help="RFC 3339 end of a same-length window before the stack was enabled")
    ap.add_argument("--retention-size", type=parse_size, default=parse_size("40GB"))
    ap.add_argument("--node-job", default="node", help="the control plane's node_exporter job")
    ap.add_argument("--shipper", default="alloy", choices=["alloy", "promtail"], help="the selected journal shipper")
    args = ap.parse_args(argv)

    prom = prom or Prometheus(args.prometheus)
    end = parse_time(args.end)
    baseline_end = parse_time(args.baseline_end) if args.baseline_end else None
    rows, failed = evaluate(prom, end, args.window, baseline_end, args.retention_size, args.node_job, args.shipper)
    width = max(len(r[1]) for r in rows)
    for status, name, detail in rows:
        print(f"{status:4}  {name:<{width}}  {detail}")
    print("NO-GO" if failed else "GO (review SKIP lines before deciding)")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
