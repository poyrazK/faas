#!/usr/bin/env python3
"""Unit tests for slo_gate.py against a fake Prometheus HTTP API."""
import contextlib
import importlib.util
import io
import json
import pathlib
import re
import sys
import threading
import unittest
import urllib.parse
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = pathlib.Path(__file__).parent
REPO = HERE.parent.parent
spec = importlib.util.spec_from_file_location("slo_gate", HERE / "slo_gate.py")
sg = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = sg  # dataclasses resolve annotations through sys.modules
spec.loader.exec_module(sg)

T0 = 1_800_000_000.0  # gate start (fake clock)
DEPLOYED = T0 - 5


def rfc3339(ts):
    return datetime.fromtimestamp(ts, timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.") + f"{ts % 1:.9f}"[2:] + "Z"


def rule(name, duration=0, component="platform", severity="page", family=None):
    labels = {"component": component, "severity": severity}
    if family:
        labels["family"] = family
    return {"type": "alerting", "name": name, "query": f"{name.lower()}_expr", "duration": duration,
            "labels": labels, "annotations": {"summary": name}, "health": "ok", "alerts": []}


def platform_rules():
    return [rule(n) for n in sg.PLATFORM_ALERTS]


class FakePrometheus:
    """/api/v1/rules returns rules_at(now) with lastEvaluation = now;
    /api/v1/query answers the scrape check with up_at(now): one up series per
    target, value "1" unless the target carries "__value"."""

    def __init__(self, clock, rules_at, up_at=lambda now: [{"job": "schedd"}]):
        self.clock, self.rules_at, self.up_at, self.requests = clock, rules_at, up_at, []
        fake = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                url = urllib.parse.urlparse(self.path)
                fake.requests.append(url.path)
                now = fake.clock.t
                try:
                    if url.path == "/api/v1/rules":
                        rules = fake.rules_at(now)
                        for r in rules:
                            r.setdefault("lastEvaluation", rfc3339(now))
                        data = {"groups": [{"name": "g", "rules": rules}]}
                    elif url.path == "/api/v1/query":
                        data = {"resultType": "vector", "result": [
                            {"metric": {k: v for k, v in m.items() if k != "__value"}, "value": [now, m.get("__value", "1")]}
                            for m in fake.up_at(now)]}
                    else:
                        raise KeyError(url.path)
                    body, code = {"status": "success", "data": data}, 200
                except Exception as err:  # noqa: BLE001 - surface as a Prometheus error
                    body, code = {"status": "error", "errorType": "internal", "error": str(err)}, 500
                raw = json.dumps(body).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def log_message(self, *args):
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def close(self):
        self.server.shutdown()
        self.server.server_close()


class Clock:
    def __init__(self):
        self.t = T0

    def now(self):
        return self.t

    def sleep(self, seconds):
        self.t += seconds


def with_alert(rules, name, active_at, labels=None, state="pending"):
    for r in rules:
        if r["name"] == name:
            r["alerts"].append({"labels": labels or {"alertname": name}, "state": state, "activeAt": rfc3339(active_at), "value": "1"})
    return rules


class GateTest(unittest.TestCase):
    def gate(self, rules_at, *args, up_at=None):
        self.clock, self.lines = Clock(), []
        kwargs = {"up_at": up_at} if up_at else {}
        prom = FakePrometheus(self.clock, rules_at, **kwargs)
        self.addCleanup(prom.close)
        if "--deployed-at" not in args:
            args = (*args, "--deployed-at", str(DEPLOYED))
        code = sg.main(["--prometheus-url", prom.url, "--duration", "2m", "--interval", "15s", *args],
                       now=self.clock.now, sleep=self.clock.sleep, log=self.lines.append)
        return code, prom

    def elapsed(self):
        return self.clock.t - T0

    def test_pass_when_nothing_is_active(self):
        code, prom = self.gate(lambda now: platform_rules(), "--unit", "schedd")
        self.assertEqual(code, sg.PASS, self.lines)
        self.assertEqual(self.elapsed(), 120)
        # Selection, then one rules read and one scrape check per round: no
        # PromQL per rule.
        self.assertEqual(prom.requests.count("/api/v1/rules"), 1 + 9)
        self.assertEqual(prom.requests.count("/api/v1/query"), 9)

    def test_new_alert_held_for_the_rules_for_fails(self):
        def rules_at(now):
            rules = platform_rules() + [rule("FaasScheddWakeErrors", duration=30, component="schedd", severity="warn")]
            return with_alert(rules, "FaasScheddWakeErrors", T0 + 15) if now >= T0 + 15 else rules

        code, _ = self.gate(rules_at, "--unit", "schedd")
        self.assertEqual(code, sg.FAIL, self.lines)
        self.assertEqual(self.elapsed(), 45)  # active at +15, held 30s
        self.assertTrue(any("FaasScheddWakeErrors" in line and "::error::" in line for line in self.lines))

    def test_alert_that_clears_passes(self):
        def rules_at(now):
            rules = platform_rules()
            rules[0]["duration"] = 60
            return with_alert(rules, rules[0]["name"], T0 + 15) if T0 + 15 <= now < T0 + 60 else rules

        code, _ = self.gate(rules_at, "--unit", "schedd")
        self.assertEqual(code, sg.PASS, self.lines)

    def test_instant_rule_fails_at_once(self):
        code, _ = self.gate(lambda now: with_alert(platform_rules(), "FaasDaemonDown", T0, {"job": "schedd"}), "--unit", "schedd")
        self.assertEqual(code, sg.FAIL, self.lines)
        self.assertEqual(self.elapsed(), 0)

    def test_alerts_true_before_the_deploy_are_ignored_but_new_ones_fail(self):
        def rules_at(now):
            rules = with_alert(platform_rules(), "FaasDaemonDown", DEPLOYED - 600, {"job": "meterd"}, state="firing")
            if now >= T0 + 30:
                with_alert(rules, "FaasDaemonDown", T0 + 30, {"job": "schedd"})
            return rules

        code, _ = self.gate(rules_at, "--unit", "schedd")
        self.assertEqual(code, sg.FAIL, self.lines)
        self.assertEqual(self.elapsed(), 30)
        self.assertEqual(sum("ignored" in line for line in self.lines), 1)
        self.assertTrue(any("'job': 'schedd'" in line for line in self.lines))

    def test_max_hold_caps_a_long_for(self):
        def rules_at(now):
            rules = platform_rules()
            rules[0]["duration"] = 600  # for: 10m
            return with_alert(rules, rules[0]["name"], T0)

        code, _ = self.gate(rules_at, "--unit", "schedd", "--max-hold", "1m")
        self.assertEqual(code, sg.FAIL, self.lines)
        self.assertEqual(self.elapsed(), 60)

    def test_unit_not_scraped_fails(self):
        code, _ = self.gate(lambda now: platform_rules(), "--unit", "schedd",
                            up_at=lambda now: [{"job": "schedd", "__value": "0"}])
        self.assertEqual(code, sg.FAIL, self.lines)
        self.assertTrue(any("no healthy scrape target" in line for line in self.lines))

    def test_job_this_prometheus_does_not_scrape_is_skipped(self):
        # production-us runs s3-gatewayd without a scrape target; the first
        # production unit release rolled back on "no healthy scrape target".
        code, _ = self.gate(lambda now: platform_rules(), "--unit", "s3-gatewayd", up_at=lambda now: [])
        self.assertEqual(code, sg.PASS, self.lines)
        self.assertEqual(sum("not scraped by this Prometheus" in line for line in self.lines), 1)

    def test_preexisting_alert_that_fires_again_after_a_restart_is_ignored(self):
        # meterd's own gauge disappears while meterd restarts, so an alert
        # that was true before the deploy resolves and fires again with a
        # new activeAt (cd-controlplane run 39473046675).
        labels = {"alertname": "FaasDaemonDown", "job": "meterd"}

        def rules_at(now):
            rules = platform_rules()
            if now < T0 + 30:
                return with_alert(rules, "FaasDaemonDown", DEPLOYED - 600, labels, state="firing")
            if now < T0 + 45:
                return rules
            return with_alert(rules, "FaasDaemonDown", T0 + 45, labels)

        code, _ = self.gate(rules_at, "--unit", "schedd")
        self.assertEqual(code, sg.PASS, self.lines)
        self.assertEqual(sum("ignored" in line for line in self.lines), 1)

    def test_runtime_unit_needs_no_scrape_target(self):
        code, prom = self.gate(lambda now: platform_rules(), "--unit", "runtime", up_at=lambda now: [])
        self.assertEqual(code, sg.PASS, self.lines)
        self.assertNotIn("/api/v1/query", prom.requests)

    def test_missing_platform_rule_is_inconclusive(self):
        code, _ = self.gate(lambda now: platform_rules()[1:], "--unit", "schedd")
        self.assertEqual(code, sg.INCONCLUSIVE, self.lines)

    def test_platform_rule_in_error_is_inconclusive(self):
        def rules_at(now):
            rules = platform_rules()
            rules[0]["health"] = "err" if now > T0 else "ok"
            return rules

        code, _ = self.gate(rules_at, "--unit", "schedd")
        self.assertEqual(code, sg.INCONCLUSIVE, self.lines)

    def test_stalled_rule_evaluation_is_inconclusive(self):
        def rules_at(now):
            rules = platform_rules()
            for r in rules:
                r["lastEvaluation"] = rfc3339(T0)
            return rules

        # Stale after max(3 intervals, 120 s), inconclusive four rounds later.
        code, _ = self.gate(rules_at, "--unit", "schedd", "--duration", "10m")
        self.assertEqual(code, sg.INCONCLUSIVE, self.lines)
        self.assertEqual(self.elapsed(), 180)

    def test_prometheus_errors_mid_gate_are_inconclusive(self):
        def rules_at(now):
            if now > T0:
                raise RuntimeError("rule manager unavailable")
            return platform_rules()

        code, _ = self.gate(rules_at, "--unit", "schedd")
        self.assertEqual(code, sg.INCONCLUSIVE, self.lines)

    def test_prometheus_unreachable_is_inconclusive(self):
        lines = []
        code = sg.main(["--unit", "schedd", "--deployed-at", "0", "--prometheus-url", "http://127.0.0.1:9"], log=lines.append)
        self.assertEqual(code, sg.INCONCLUSIVE, lines)

    def test_usage_errors(self):
        quiet = lambda *_: None  # noqa: E731
        self.assertEqual(sg.main(["--unit", "nope", "--deployed-at", "0"], log=quiet), sg.USAGE)
        self.assertEqual(sg.main(["--unit", "x", "--job", 'a"b', "--deployed-at", "0"], log=quiet), sg.USAGE)
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(sg.main(["--unit", "schedd"], log=quiet), sg.USAGE)

    def test_list(self):
        code, prom = self.gate(lambda now: platform_rules(), "--unit", "vmmd", "--list")
        self.assertEqual(code, sg.PASS)
        self.assertEqual(prom.requests, ["/api/v1/rules"])
        self.assertEqual(len(self.lines), len(sg.PLATFORM_ALERTS))


class HelpersTest(unittest.TestCase):
    def test_select_platform_and_own_page_and_warn_rules(self):
        loaded = platform_rules() + [
            rule("ScheddPage", component="schedd", severity="page"),
            rule("ScheddWarn", component="schedd", severity="warn"),
            rule("ScheddInfo", component="schedd", severity="info"),
            rule("VmmdPage", component="vmmd", severity="page"),
            rule("ScheddPage", component="schedd", severity="page"),  # same rule in two groups
        ]
        rules, missing = sg.select_rules(loaded, ["schedd"], max_hold=120)
        self.assertEqual(missing, [])
        self.assertEqual(sorted(r.name for r in rules), sorted([*sg.PLATFORM_ALERTS, "ScheddPage", "ScheddWarn"]))

    def test_tenant_signal_families_do_not_gate_a_unit(self):
        loaded = platform_rules() + [
            rule("ApidDown", component="apid", severity="page", family="daemon_liveness"),
            *[rule(f"Apid{f}", component="apid", severity="warn", family=f) for f in sg.TENANT_SIGNAL_FAMILIES],
        ]
        rules, _ = sg.select_rules(loaded, ["apid"], max_hold=120)
        self.assertEqual(sorted(r.name for r in rules), sorted([*sg.PLATFORM_ALERTS, "ApidDown"]))

    def test_parse_time(self):
        self.assertAlmostEqual(sg.parse_time("2026-10-17T15:09:13.123456789Z"),
                               datetime(2026, 10, 17, 15, 9, 13, tzinfo=timezone.utc).timestamp() + 0.123456789, places=6)
        self.assertEqual(sg.parse_time("2026-10-17T17:09:13+02:00"), datetime(2026, 10, 17, 15, 9, 13, tzinfo=timezone.utc).timestamp())
        self.assertIsNone(sg.parse_time("0001-01-01T00:00:00Z"))
        self.assertIsNone(sg.parse_time(""))


class RepositoryContractTest(unittest.TestCase):
    """The gate names alerts, components and scrape jobs; they must exist."""

    def setUp(self):
        files = (REPO / "deploy/ansible/roles/prometheus/files").glob("*.rules.yml")
        text = "\n".join(p.read_text(encoding="utf-8") for p in files if not p.name.endswith(".test.yml"))
        self.alerts = set(re.findall(r"^\s*- alert:\s*(\S+)", text, re.MULTILINE))
        self.components = set(re.findall(r"^\s*component:\s*(\S+)", text, re.MULTILINE))
        self.families = set(re.findall(r"^\s*family:\s*(\S+)", text, re.MULTILINE))
        prom = REPO / "deploy/ansible/roles/prometheus"
        jobs = (prom / "defaults/main.yml").read_text(encoding="utf-8") + (prom / "templates/prometheus.yml.j2").read_text(encoding="utf-8")
        self.jobs = set(re.findall(r"(?:- job|job_name):\s*\"?([\w-]+)\"?", jobs))

    def test_platform_alerts_exist(self):
        self.assertEqual([a for a in sg.PLATFORM_ALERTS if a not in self.alerts], [])

    def test_unit_components_exist(self):
        for unit, cfg in sg.UNITS.items():
            for c in cfg["components"]:
                self.assertIn(c, self.components, f"{unit}: no alert has component {c}")

    def test_tenant_signal_families_exist(self):
        self.assertEqual([f for f in sg.TENANT_SIGNAL_FAMILIES if f not in self.families], [])

    def test_unit_jobs_exist(self):
        for unit, cfg in sg.UNITS.items():
            for job in cfg["jobs"]:
                self.assertIn(job, self.jobs, f"{unit}: no scrape job {job}")


if __name__ == "__main__":
    unittest.main()
