#!/usr/bin/env python3
"""Go/no-go check for observability on the control plane (ADR-20261023124955 §7).

Run on a control plane after the observability stack has run for the
measurement window (72 h by default). Reads only Prometheus, which holds the
per-slice numbers faas-cgroup-metrics writes, and prints one line per
check: PASS or FAIL. Missing or incomplete evidence is a failure, not a
successful gate. Exit code 1 when any check fails, 0 otherwise.

Relative checks (PostgreSQL CPU stall, platform wake p95) compare the window
with a baseline window of the same length that ends at --baseline-end, taken
before the stack was enabled. Both windows need at least 99% fresh evidence
at one-minute resolution. A production gate requires at least 72 hours and
a non-overlapping baseline; shorter runs still print diagnostic results but
cannot return GO.

    scripts/ops/obs_gate.py --window 72h --baseline-end 2026-10-20T00:00:00Z
"""

import argparse
import datetime as dt
import json
import math
import re
import sys
import urllib.parse
import urllib.request

SHIPPER_JOBS = 'job="promtail"'  # The control-plane shipper, including Alloy.
MIN_WINDOW = 72 * 3600
MIN_COVERAGE = 0.99
UNITS = {"s": 1, "m": 60, "h": 3600, "d": 86400}
SIZE_UNITS = {"B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12}


def parse_duration(text):
    m = re.fullmatch(r"(\d+)([smhd])", text)
    if not m or int(m.group(1)) == 0:
        raise argparse.ArgumentTypeError(f"duration like 72h expected, got {text!r}")
    return int(m.group(1)) * UNITS[m.group(2)]


def parse_size(text):
    m = re.fullmatch(r"(\d+)(B|KB|MB|GB|TB)", text)
    if not m or int(m.group(1)) == 0:
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
        return value if math.isfinite(value) else None


def shipper_metrics(shipper):
    if shipper == "alloy":
        return "loki_source_journal_target_lines_total", "loki_write_dropped_entries_total"
    return "promtail_journal_target_lines_total", "promtail_dropped_entries_total"


def checks(window, retention_size, node_job="node", shipper="alloy"):
    """(name, query, comparator, threshold, unit, relative).

    Per-slice numbers are pinned to the control plane's node_exporter job:
    compute nodes run a faas-obs.slice too. Every check requires evidence.
    """
    w = f"{window}s"
    obs = f'job="{node_job}",cgroup="faas-obs.slice"'
    pg = f'job="{node_job}",cgroup="system.slice/system-postgresql.slice"'
    ship = f'job="{node_job}",cgroup="faas-obs.slice/{shipper}.service"'
    read_metric, drop_metric = shipper_metrics(shipper)
    return [
        ("obs slice CPU, window average", f"avg_over_time(rate(faas_cgroup_cpu_usage_seconds_total{{{obs}}}[5m])[{w}:1m])", "<=", 0.25, "cores", False),
        ("obs slice CPU, worst 5 min", f"max_over_time(rate(faas_cgroup_cpu_usage_seconds_total{{{obs}}}[5m])[{w}:1m])", "<=", 0.60, "cores", False),
        ("obs slice anon memory peak / limit", f"max_over_time(faas_cgroup_memory_anon_bytes{{{obs}}}[{w}]) / faas_cgroup_memory_max_bytes{{{obs}}}", "<=", 0.80, "ratio", False),
        ("obs slice OOM kills", f'increase(faas_cgroup_memory_events_total{{{obs},event="oom_kill"}}[{w}])', "<=", 0, "kills", False),
        # ADR-20261023185953 Decision 5: the shipper's own budget on the control plane.
        (f"{shipper} peak anon memory", f"max_over_time(faas_cgroup_memory_anon_bytes{{{ship}}}[{w}])", "<=", 200e6, "bytes", False),
        (f"{shipper} CPU, window average", f"avg_over_time(rate(faas_cgroup_cpu_usage_seconds_total{{{ship}}}[5m])[{w}:1m])", "<=", 0.05, "cores", False),
        # The stalled-shipping alert needs a read counter. Alloy's name was not
        # confirmed when the rules were written, so its absence is a failure.
        ("journal read counter present", f"count({read_metric}{{{SHIPPER_JOBS}}})", ">=", 1, "series", False),
        ("Prometheus 35 d disk projection", "max(prometheus_tsdb_storage_blocks_bytes) / ((time() - min(prometheus_tsdb_lowest_timestamp_seconds)) / 86400) * 35", "<=", retention_size, "bytes", False),
        # Drop counters can be created lazily. A healthy local scrape is the
        # only permitted zero fallback; its history is checked separately.
        ("journal shipper dropped entries", f"sum(increase({drop_metric}{{{SHIPPER_JOBS}}}[{w}])) or (0 * min(up{{{SHIPPER_JOBS}}} == 1))", "<=", 0, "entries", False),
        ("PostgreSQL CPU stall vs baseline", f'avg_over_time(rate(faas_cgroup_pressure_stall_seconds_total{{{pg},resource="cpu",kind="some"}}[5m])[{w}:1m])', "delta<=", 0.05, "share", True),
        ("platform wake p95 vs baseline", f"histogram_quantile(0.95, sum by (le) (increase(gateway_platform_wake_latency_seconds_bucket[{w}])))", "ratio<=", 1.10, "s", True),
    ]


def evidence_queries(window, node_job="node", shipper="alloy"):
    """Fresh input coverage, rather than timestamps on aggregate results.

    A one-minute grid bounds the query cost. Missing minutes count against
    coverage, including when Prometheus has only a fraction of the window.
    The collector's own timestamp also detects a frozen textfile that keeps
    being scraped. Only baseline inputs need baseline coverage.
    """
    obs = f'job="{node_job}",cgroup="faas-obs.slice"'
    pg = f'job="{node_job}",cgroup="system.slice/system-postgresql.slice",resource="cpu",kind="some"'
    ship = f'job="{node_job}",cgroup="faas-obs.slice/{shipper}.service"'
    read_metric, _ = shipper_metrics(shipper)
    sources = [
        ("cgroup collector", f'faas_cgroup_metrics_last_run_timestamp_seconds{{job="{node_job}"}}', True, True),
        ("obs CPU", f"faas_cgroup_cpu_usage_seconds_total{{{obs}}}", False, False),
        ("obs memory", f"faas_cgroup_memory_anon_bytes{{{obs}}}", False, False),
        ("obs memory limit", f"faas_cgroup_memory_max_bytes{{{obs}}}", False, False),
        ("obs OOM counter", f'faas_cgroup_memory_events_total{{{obs},event="oom_kill"}}', False, False),
        ("shipper CPU", f"faas_cgroup_cpu_usage_seconds_total{{{ship}}}", False, False),
        ("shipper memory", f"faas_cgroup_memory_anon_bytes{{{ship}}}", False, False),
        ("journal read counter", f"{read_metric}{{{SHIPPER_JOBS}}}", False, False),
        ("shipper scrape", f'up{{{SHIPPER_JOBS}}} == 1', False, False),
        ("Prometheus storage", "prometheus_tsdb_storage_blocks_bytes", False, False),
        ("Prometheus oldest sample", "prometheus_tsdb_lowest_timestamp_seconds", False, False),
        ("PostgreSQL CPU stall", f"faas_cgroup_pressure_stall_seconds_total{{{pg}}}", False, True),
        ("platform wake histogram", "gateway_platform_wake_latency_seconds_bucket", False, True),
    ]
    for name, selector, is_clock, baseline in sources:
        observed = selector if is_clock else f"timestamp({selector})"
        fresh = f"({observed} >= time() - 120) and ({observed} <= time() + 30)"
        query = (
            f"(min(count_over_time(({fresh})[{window}s:1m]))"
            f" / scalar(count_over_time(vector(1)[{window}s:1m])))"
            f" and (count({fresh}) == count({observed}))"
        )
        yield name, query, baseline


def evaluate(prom, end, window, baseline_end, retention_size, node_job="node", shipper="alloy"):
    rows, failed = [], False
    for name, query, op, limit, unit, relative in checks(window, retention_size, node_job, shipper):
        value = prom.scalar(query, end)
        base = prom.scalar(query, baseline_end) if relative and baseline_end is not None else None
        if value is None or not math.isfinite(value) or (relative and (base is None or not math.isfinite(base))):
            reason = "no baseline; supply --baseline-end" if relative and baseline_end is None else "missing or non-finite evidence"
            rows.append(("FAIL", name, reason))
            failed = True
            continue
        if op == "<=":
            ok, shown = value <= limit, f"{value:.4g} {unit} (limit {limit:.4g})"
        elif op == ">=":
            ok, shown = value >= limit, f"{value:.4g} {unit} (at least {limit:.4g})"
        elif op == "delta<=":
            ok, shown = value - base <= limit, f"{value:.4g} vs baseline {base:.4g} (allowed +{limit})"
        else:
            ok = base > 0 and value / base <= limit
            shown = f"{value:.4g} {unit} vs baseline {base:.4g} (allowed x{limit})"
        failed |= not ok
        rows.append(("PASS" if ok else "FAIL", name, shown))
    for name, query, needs_baseline in evidence_queries(window, node_job, shipper):
        windows = [("measurement", end)]
        if needs_baseline:
            windows.append(("baseline", baseline_end))
        for label, at in windows:
            coverage = prom.scalar(query, at) if at is not None else None
            ok = coverage is not None and math.isfinite(coverage) and coverage >= MIN_COVERAGE
            shown = f"{coverage:.2%} (required {MIN_COVERAGE:.0%})" if coverage is not None else "missing fresh history"
            rows.append(("PASS" if ok else "FAIL", f"{name} {label} coverage", shown))
            failed |= not ok
    for name, ok, detail in [
        ("measurement duration", window >= MIN_WINDOW, "at least 72h required for GO"),
        ("baseline precedes measurement", baseline_end is not None and baseline_end <= end - window, "supply a baseline ending before the measurement window"),
    ]:
        rows.append(("PASS" if ok else "FAIL", name, detail))
        failed |= not ok
    return rows, failed


def main(argv=None, prom=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--prometheus", default="http://127.0.0.1:9095")
    ap.add_argument("--end", default="now", help="RFC 3339 end of the measurement window, or now")
    ap.add_argument("--window", type=parse_duration, default=parse_duration("72h"))
    ap.add_argument("--baseline-end", help="required for GO: RFC 3339 end of a same-length window before the stack was enabled")
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
    print("NO-GO" if failed else "GO")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
