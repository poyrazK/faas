#!/usr/bin/env python3
"""Tests for the observability go/no-go check (ADR-20261023124955 §7)."""

import contextlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import obs_gate  # noqa: E402

END = obs_gate.parse_time("2026-10-26T00:00:00Z")
BASE = obs_gate.parse_time("2026-10-20T00:00:00Z")


class FakeProm:
    """Answers by the first matching query fragment; at selects window vs baseline."""

    def __init__(self, window, baseline=None, coverage=1.0, baseline_coverage=1.0):
        self.window, self.baseline = window, baseline or {}
        self.coverage, self.baseline_coverage = coverage, baseline_coverage
        self.queries = []

    def scalar(self, query, at):
        self.queries.append(query)
        if query.startswith("(min(count_over_time("):
            return self.coverage if at == END else self.baseline_coverage
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
    "loki_write_dropped_entries_total": 0,
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
        self.assertEqual(count(out, "FAIL"), 0, out)
        self.assertEqual(out.splitlines()[-1], "GO")

    def test_each_limit_fails_the_gate(self):
        cases = {
            'max_over_time(faas_cgroup_memory_anon_bytes{job="node",cgroup="faas-obs.slice/alloy.service"}': 230e6,
            'avg_over_time(rate(faas_cgroup_cpu_usage_seconds_total{job="node",cgroup="faas-obs.slice/alloy.service"}': 0.09,
            "avg_over_time(rate(faas_cgroup_cpu_usage_seconds_total": 0.30,
            "max_over_time(rate(faas_cgroup_cpu_usage_seconds_total": 0.75,
            "max_over_time(faas_cgroup_memory_anon_bytes": 0.86,
            "event=\"oom_kill\"": 1,
            "prometheus_tsdb_storage_blocks_bytes": 45e9,
            "loki_write_dropped_entries_total": 12,
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

    def test_missing_baseline_cannot_approve_a_rollout(self):
        code, out = run(FakeProm(HEALTHY))
        self.assertEqual(code, 1, out)
        self.assertEqual(count(out, "SKIP"), 0, out)
        self.assertIn("no baseline", out)
        self.assertEqual(out.splitlines()[-1], "NO-GO")

    def test_each_missing_measurement_fails_closed(self):
        for fragment in HEALTHY:
            with self.subTest(fragment=fragment):
                window = dict(HEALTHY, **{fragment: None})
                code, out = run(FakeProm(window, BASELINE), "--baseline-end", "2026-10-20T00:00:00Z")
                self.assertEqual(code, 1, out)
                self.assertIn("missing or non-finite evidence", out)
                self.assertEqual(out.splitlines()[-1], "NO-GO")

    def test_a_missing_read_counter_fails_closed(self):
        window = {k: v for k, v in HEALTHY.items() if "journal_target_lines" not in k}
        code, out = run(FakeProm(window, BASELINE), "--baseline-end", "2026-10-20T00:00:00Z")
        self.assertEqual(code, 1, out)
        self.assertRegex(out, r"FAIL\s+journal read counter present\s+missing or non-finite evidence")

    def test_only_a_read_counter_is_not_sufficient_evidence(self):
        code, out = run(FakeProm({"loki_source_journal_target_lines_total": 1}, coverage=None))
        self.assertEqual(code, 1, out)
        self.assertEqual(count(out, "PASS"), 2, out)  # Counter and duration only.
        self.assertEqual(out.splitlines()[-1], "NO-GO")

    def test_missing_or_partial_history_fails_even_with_healthy_values(self):
        for coverage in (None, 0.0, 0.98, float("nan"), float("inf")):
            for field in ("coverage", "baseline_coverage"):
                with self.subTest(coverage=coverage, field=field):
                    prom = FakeProm(HEALTHY, BASELINE, **{field: coverage})
                    code, out = run(prom, "--baseline-end", "2026-10-20T00:00:00Z")
                    self.assertEqual(code, 1, out)
                    self.assertRegex(out, r"FAIL\s+.*coverage")

    def test_history_threshold_accepts_99_percent(self):
        prom = FakeProm(HEALTHY, BASELINE, coverage=0.99, baseline_coverage=0.99)
        code, out = run(prom, "--baseline-end", "2026-10-20T00:00:00Z")
        self.assertEqual(code, 0, out)

    def test_short_measurements_and_overlapping_baselines_cannot_approve(self):
        for argv in (("--window", "60m"), ("--baseline-end", "2026-10-25T00:00:00Z")):
            with self.subTest(argv=argv):
                code, out = run(FakeProm(HEALTHY, BASELINE), "--baseline-end", "2026-10-20T00:00:00Z", *argv)
                self.assertEqual(code, 1, out)
                self.assertEqual(out.splitlines()[-1], "NO-GO")

    def test_missing_non_finite_and_zero_latency_baselines_fail(self):
        for value in (None, float("nan"), float("inf"), 0.0):
            with self.subTest(value=value):
                baseline = dict(BASELINE, gateway_platform_wake_latency_seconds_bucket=value)
                code, out = run(FakeProm(HEALTHY, baseline), "--baseline-end", "2026-10-20T00:00:00Z")
                self.assertEqual(code, 1, out)
                self.assertRegex(out, r"FAIL\s+platform wake p95 vs baseline")

    def test_non_finite_prometheus_samples_are_unavailable(self):
        class Opener:
            def open(self, url, timeout):
                return io.BytesIO(b'{"status":"success","data":{"result":[{"value":[0,"' + sample.encode() + b'"]}]}}')

        for sample in ("NaN", "+Inf", "-Inf"):
            with self.subTest(sample=sample):
                self.assertIsNone(obs_gate.Prometheus("http://localhost", opener=Opener()).scalar("query", END))

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

    def test_shipper_evidence_is_local_and_matches_the_selected_shipper(self):
        for shipper, prefix in (("alloy", "loki_"), ("promtail", "promtail_")):
            with self.subTest(shipper=shipper):
                queries = [q for _, q, *_ in obs_gate.checks(259200, 40e9, shipper=shipper)]
                queries += [q for _, q, _ in obs_gate.evidence_queries(259200, shipper=shipper)]
                journal = [q for q in queries if "journal_target_lines_total" in q]
                self.assertTrue(journal and all(prefix in q and 'job="promtail"' in q for q in journal), journal)
                self.assertFalse(any("promtail-compute" in q for q in queries))

    def test_argument_parsing(self):
        self.assertEqual(obs_gate.parse_duration("72h"), 259200)
        self.assertEqual(obs_gate.parse_size("40GB"), 40e9)
        with self.assertRaises(Exception):
            obs_gate.parse_duration("3 days")
        for parser, value in ((obs_gate.parse_duration, "0h"), (obs_gate.parse_size, "0GB")):
            with self.assertRaises(Exception):
                parser(value)


PROMTOOL = os.environ.get("PROMTOOL") or shutil.which("promtool")


@unittest.skipUnless(PROMTOOL, "promtool is required for real PromQL evaluation")
class ObsGatePromQLTest(unittest.TestCase):
    def invoke(self, subcommand, data):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "fixture.yml"
            path.write_text(json.dumps(data))  # JSON is also valid YAML.
            result = subprocess.run([PROMTOOL, *subcommand, str(path)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_all_generated_queries_parse(self):
        queries = [q for _, q, *_ in obs_gate.checks(600, 40e9)]
        queries += [q for _, q, _ in obs_gate.evidence_queries(600)]
        self.invoke(["check", "rules"], {"groups": [{"name": "obs_gate", "rules": [
            {"record": f"obs_gate_check_{i}", "expr": query} for i, query in enumerate(queries)
        ]}]})

    def test_history_uses_actual_sample_freshness_and_the_full_window(self):
        queries = {name: query for name, query, _ in obs_gate.evidence_queries(600)}
        clock = 'faas_cgroup_metrics_last_run_timestamp_seconds{job="node"}'
        cases = [
            ("complete", "cgroup collector", [(clock, "0+60x10")], 1.0),
            ("new series", "cgroup collector", [(clock, "_ _ _ _ _ _ _ _ 480 540 600")], 3 / 11),
            ("missing series", "cgroup collector", [], None),
            ("frozen textfile", "cgroup collector", [(clock, "0x10")], None),
            ("future clock", "cgroup collector", [(clock, "300+60x10")], None),
            ("collector recovered after gap", "cgroup collector", [(clock, "0+60x2 120x5 480+60x2")], 7 / 11),
            ("one stale collector", "cgroup collector", [
                (clock, "0+60x10"),
                ('faas_cgroup_metrics_last_run_timestamp_seconds{job="node",instance="stale"}', "0x10"),
            ], None),
            ("scrape recovered after outage", "shipper scrape", [('up{job="promtail"}', "1x2 0x5 1x2")], 5 / 11),
            ("scrape currently down", "shipper scrape", [('up{job="promtail"}', "1x9 0")], None),
            ("compute scrape cannot replace local", "shipper scrape", [('up{job="promtail-compute"}', "1x10")], None),
        ]
        tests = []
        for name, source, series, expected in cases:
            samples = [] if expected is None else [{"labels": "{}", "value": expected}]
            tests.append({"name": name, "interval": "1m", "input_series": [
                {"series": metric, "values": values} for metric, values in series
            ], "promql_expr_test": [{"expr": queries[source], "eval_time": "10m", "exp_samples": samples}]})
        self.invoke(["test", "rules"], {"evaluation_interval": "1m", "tests": tests})

    def test_lazy_drop_counter_zero_requires_a_healthy_local_scrape(self):
        query = next(q for name, q, *_ in obs_gate.checks(600, 40e9) if name == "journal shipper dropped entries")
        tests = []
        for name, series, expected in [
            ("healthy local", [('up{job="promtail"}', "1x10")], 0),
            ("local down", [('up{job="promtail"}', "0x10")], None),
            ("local missing", [], None),
            ("only compute healthy", [('up{job="promtail-compute"}', "1x10")], None),
        ]:
            tests.append({"name": name, "interval": "1m", "input_series": [
                {"series": metric, "values": values} for metric, values in series
            ], "promql_expr_test": [{"expr": query, "eval_time": "10m", "exp_samples":
                [] if expected is None else [{"labels": "{}", "value": expected}]}]})
        self.invoke(["test", "rules"], {"evaluation_interval": "1m", "tests": tests})


if __name__ == "__main__":
    unittest.main()
