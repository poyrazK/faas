#!/usr/bin/env python3
"""Evaluate receiver evidence and dashboard freshness in the real rule engine."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
PROMTOOL = os.environ.get("PROMTOOL") or shutil.which("promtool")
PAIR = {"receiver": "faas-page", "integration": "slack"}


def metric(name, **labels):
    return name + "{" + ",".join(f"{key}={json.dumps(value)}" for key, value in labels.items()) + "}"


def raw(name, paired=False, instance="cp:9100"):
    return metric("faas_notification_receiver_" + name, job="node", instance=instance, **(PAIR if paired else {}))


def healthy():
    return {
        metric("up", job="node", instance="cp:9100"): "1x40",
        metric("up", job="alertmanager", instance="cp:9094"): "1x40",
        raw("collector_up"): "1x40",
        raw("collector_last_run_timestamp_seconds"): "0+60x40",
        raw("expected", True): "1x40",
        raw("requests_total", True): "10x40",
        raw("failures_total", True): "0x40",
        raw("latency_seconds_total", True): "1x40",
        raw("last_success_observed_timestamp_seconds", True): "1200x40",
    }


@unittest.skipUnless(PROMTOOL, "promtool is required")
class ReceiverRulesTest(unittest.TestCase):
    def invoke(self, tests, rules=True):
        data = {"evaluation_interval": "1m", "tests": tests}
        if rules:
            data["rule_files"] = [str(ROOT / "deploy/ansible/roles/prometheus/files/faas.rules.yml")]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "receivers.test.yml"
            path.write_text(json.dumps(data))
            result = subprocess.run([PROMTOOL, "test", "rules", str(path)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_missing_stale_partial_and_wrong_source_evidence(self):
        base = healthy()
        cases = [("fresh evidence", base, 1, True)]
        cold = dict(base, **{raw("last_success_observed_timestamp_seconds", True): "0x40"})
        cases.append(("unknown receipt still has valid counter evidence", cold, 1, True))
        for name, value in [("collector_up", "0x40"), ("collector_last_run_timestamp_seconds", "1x40"),
                            ("collector_last_run_timestamp_seconds", "3600+60x40")]:
            cases.append(("failed, frozen or future collector: " + value, dict(base, **{raw(name): value}), 0, False))
        absent = {key: value for key, value in base.items() if "collector_" not in key and "_total" not in key and "last_success" not in key}
        cases.append(("never installed", absent, 0, False))
        duplicate = dict(base, **{raw("collector_up", instance="other:9100"): "1x40"})
        cases.append(("duplicate source cannot establish health", duplicate, 0, False))
        wrong_clock = dict(base)
        wrong_clock[raw("collector_last_run_timestamp_seconds", instance="other:9100")] = wrong_clock.pop(raw("collector_last_run_timestamp_seconds"))
        cases.append(("another source's clock is insufficient", wrong_clock, 0, False))
        for field in ("requests_total", "failures_total", "latency_seconds_total", "last_success_observed_timestamp_seconds"):
            missing = dict(base)
            missing.pop(raw(field, True))
            cases.append(("incomplete group: " + field, missing, 1, False))
        stale = dict(base, **{raw("requests_total", True): "10x2"})
        cases.append(("old request samples cannot pass", stale, 1, False))
        wrong = dict(base)
        wrong[raw("failures_total", True, instance="other:9100")] = wrong.pop(raw("failures_total", True))
        cases.append(("another target's failure counter is insufficient", wrong, 1, False))
        second = dict(base, **{metric("faas_notification_receiver_expected", job="node", instance="cp:9100", receiver="faas-warn", integration="slack"): "1x40"})
        cases.append(("a sibling cannot satisfy an expected receiver", second, 1, True))
        tests = []
        for name, inputs, fresh, present in cases:
            tests.append({"name": name, "interval": "1m",
                          "input_series": [{"series": key, "values": value} for key, value in inputs.items()],
                          "promql_expr_test": [
                              {"expr": "faas_notification_receiver:collector_fresh", "eval_time": "30m",
                               "exp_samples": [{"labels": "faas_notification_receiver:collector_fresh{}", "value": fresh}]},
                              {"expr": "faas_notification_receiver:telemetry_present", "eval_time": "30m",
                               "exp_samples": [{"labels": metric("faas_notification_receiver:telemetry_present", **PAIR), "value": 1}] if present else []},
                          ]})
        self.invoke(tests)

    def test_dashboard_rejects_old_recordings_and_preserves_unknown_receipts(self):
        path = ROOT / "deploy/grafana/faas-fleet.json"
        self.assertEqual(path.read_bytes(), (ROOT / "deploy/ansible/roles/grafana/files/faas-fleet.json").read_bytes())
        panels = {panel["id"]: panel for panel in json.loads(path.read_text())["panels"] if panel["id"] in (450, 451, 452, 453)}
        self.assertEqual(len(panels), 4)
        self.assertTrue(all(panel["targets"][0]["instant"] for panel in panels.values()))
        inputs = {
            "faas_notification_receiver:collector_fresh{}": "1x2",
            metric("faas_notification_receiver:source_up", job="node", instance="cp:9100"): "1x2",
            metric("faas_notification_receiver:telemetry_present", **PAIR): "1x2",
            metric("faas_notification_receiver:expected", **PAIR): "1x2",
            raw("last_success_observed_timestamp_seconds", True): "60x2",
            raw("failures_total", True): "0x12",
            raw("requests_total", True): "0+1x12",
            raw("latency_seconds_total", True): "0+0.1x12",
        }
        checks = [{"expr": panel["targets"][0]["expr"], "eval_time": "5m", "exp_samples": []} for panel in panels.values()]
        tests = [{"name": "recording evaluation stopped: every panel loses old evidence", "interval": "1m",
                  "input_series": [{"series": key, "values": value} for key, value in inputs.items()], "promql_expr_test": checks}]
        for label, last, value in [("unknown baseline", 0, -1), ("observed success age", 60, 60),
                                   ("accepted clock skew cannot collide with unknown sentinels", 150, 0)]:
            current = dict(inputs, **{raw("last_success_observed_timestamp_seconds", True): f"{last}x2"})
            tests.append({"name": label, "interval": "1m",
                          "input_series": [{"series": key, "values": values} for key, values in current.items()],
                          "promql_expr_test": [{"expr": panels[451]["targets"][0]["expr"], "eval_time": "2m",
                                                "exp_samples": [{"labels": metric("", **PAIR), "value": value}]}]})
        missing = {key: value for key, value in inputs.items() if "telemetry_present" not in key and "last_success_observed" not in key}
        tests.append({"name": "configured group has no complete evidence", "interval": "1m",
                      "input_series": [{"series": key, "values": value} for key, value in missing.items()],
                      "promql_expr_test": [{"expr": panels[451]["targets"][0]["expr"], "eval_time": "2m",
                                            "exp_samples": [{"labels": metric("", **PAIR), "value": -2}]}]})
        self.invoke(tests, rules=False)


if __name__ == "__main__":
    unittest.main()
