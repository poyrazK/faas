#!/usr/bin/env python3
"""Tests for the observability go/no-go check (ADR-20261023124955 §7)."""

import contextlib
import io
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import obs_gate  # noqa: E402

END = obs_gate.parse_time("2026-10-26T00:00:00Z")
BASE = obs_gate.parse_time("2026-10-20T00:00:00Z")


class FakeProm:
    """Answers by the first matching query fragment; at selects window vs baseline."""

    def __init__(self, window, baseline=None):
        self.window, self.baseline = window, baseline or {}
        self.queries = []

    def scalar(self, query, at):
        self.queries.append(query)
        table = self.window if at == END else self.baseline
        for fragment, value in table.items():
            if fragment in query:
                return value
        return None


HEALTHY = {
    # Shipper checks first: their fragments are the more specific ones.
    'max_over_time(faas_cgroup_memory_anon_bytes{job="node",cgroup="faas-obs.slice/alloy.service"}': 95e6,
    'avg_over_time(rate(faas_cgroup_cpu_usage_seconds_total{job="node",cgroup="faas-obs.slice/alloy.service"}': 0.02,
    "avg_over_time(rate(faas_cgroup_cpu_usage_seconds_total": 0.12,
    "max_over_time(rate(faas_cgroup_cpu_usage_seconds_total": 0.41,
    "max_over_time(faas_cgroup_memory_anon_bytes": 0.55,
    "event=\"oom_kill\"": 0,
    "loki_source_journal_target_lines_total": 2,
    "prometheus_tsdb_storage_blocks_bytes": 28e9,
    "promtail_dropped_entries_total": 0,
    "faas_cgroup_pressure_stall_seconds_total": 0.21,
    "gateway_platform_wake_latency_seconds_bucket": 0.33,
}
BASELINE = {
    "faas_cgroup_pressure_stall_seconds_total": 0.18,
    "gateway_platform_wake_latency_seconds_bucket": 0.31,
}


def count(out, status):
    return sum(1 for line in out.splitlines() if line.startswith(status + " "))


def run(prom, *argv):
    out = io.StringIO()
    with contextlib.redirect_stdout(out):
        code = obs_gate.main(["--end", "2026-10-26T00:00:00Z", *argv], prom=prom)
    return code, out.getvalue()


class ObsGateTest(unittest.TestCase):
    def test_healthy_window_is_go(self):
        code, out = run(FakeProm(HEALTHY, BASELINE), "--baseline-end", "2026-10-20T00:00:00Z")
        self.assertEqual(code, 0, out)
        self.assertEqual(count(out, "PASS"), 11, out)
        self.assertIn("GO", out.splitlines()[-1])

    def test_each_limit_fails_the_gate(self):
        cases = {
            'max_over_time(faas_cgroup_memory_anon_bytes{job="node",cgroup="faas-obs.slice/alloy.service"}': 230e6,
            'avg_over_time(rate(faas_cgroup_cpu_usage_seconds_total{job="node",cgroup="faas-obs.slice/alloy.service"}': 0.09,
            "avg_over_time(rate(faas_cgroup_cpu_usage_seconds_total": 0.30,
            "max_over_time(rate(faas_cgroup_cpu_usage_seconds_total": 0.75,
            "max_over_time(faas_cgroup_memory_anon_bytes": 0.86,
            "event=\"oom_kill\"": 1,
            "prometheus_tsdb_storage_blocks_bytes": 45e9,
            "promtail_dropped_entries_total": 12,
            "faas_cgroup_pressure_stall_seconds_total": 0.30,  # +0.12 over baseline
            "gateway_platform_wake_latency_seconds_bucket": 0.40,  # x1.29
        }
        for fragment, bad in cases.items():
            with self.subTest(fragment=fragment):
                window = dict(HEALTHY, **{fragment: bad})
                code, out = run(FakeProm(window, BASELINE), "--baseline-end", "2026-10-20T00:00:00Z")
                self.assertEqual(code, 1, out)
                self.assertEqual(count(out, "FAIL"), 1, out)
                self.assertEqual(out.splitlines()[-1], "NO-GO")

    def test_relative_checks_skip_without_a_baseline(self):
        code, out = run(FakeProm(HEALTHY))
        self.assertEqual(code, 0, out)
        self.assertEqual(count(out, "SKIP"), 2, out)
        self.assertIn("no baseline", out)

    def test_missing_series_skip_rather_than_pass(self):
        window = {k: v for k, v in HEALTHY.items() if "promtail_dropped" not in k}
        code, out = run(FakeProm(window, BASELINE), "--baseline-end", "2026-10-20T00:00:00Z")
        self.assertEqual(code, 0, out)
        self.assertRegex(out, r"SKIP\s+journal shipper dropped entries\s+no data")

    def test_a_missing_read_counter_fails_closed(self):
        window = {k: v for k, v in HEALTHY.items() if "journal_target_lines" not in k}
        code, out = run(FakeProm(window, BASELINE), "--baseline-end", "2026-10-20T00:00:00Z")
        self.assertEqual(code, 1, out)
        self.assertRegex(out, r"FAIL\s+journal read counter present\s+no data; this series is required")

    def test_cgroup_queries_are_pinned_to_the_control_plane_job(self):
        prom = FakeProm(HEALTHY, BASELINE)
        run(prom, "--node-job", "node-cp", "--shipper", "promtail")
        cgroup = [q for q in prom.queries if "faas_cgroup_" in q]
        self.assertTrue(cgroup and all('job="node-cp"' in q for q in cgroup), cgroup)
        self.assertTrue(any("faas-obs.slice/promtail.service" in q for q in cgroup), cgroup)

    def test_queries_use_the_requested_window(self):
        prom = FakeProm(HEALTHY, BASELINE)
        run(prom, "--window", "24h", "--baseline-end", "2026-10-20T00:00:00Z")
        self.assertTrue(any("[86400s:1m]" in q for q in prom.queries), prom.queries)

    def test_argument_parsing(self):
        self.assertEqual(obs_gate.parse_duration("72h"), 259200)
        self.assertEqual(obs_gate.parse_size("40GB"), 40e9)
        with self.assertRaises(Exception):
            obs_gate.parse_duration("3 days")


if __name__ == "__main__":
    unittest.main()
