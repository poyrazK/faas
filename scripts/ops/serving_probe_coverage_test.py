#!/usr/bin/env python3
"""Evaluate serving-probe coverage against the real Prometheus rule engine."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
PROMTOOL = os.environ.get("PROMTOOL") or shutil.which("promtool")
URL = "https://probe.example/"
OTHER_URL = "https://second.example/"
STEPS = "x60"


def metric(name, **labels):
    return name + "{" + ",".join(f"{k}={json.dumps(v)}" for k, v in labels.items()) + "}"


def intent(name, **labels):
    return metric("faas_serving_probe_" + name, job="node", instance="cp:9100", **labels)


def observed(name, node="fsn-2", url=None, instance=None):
    labels = {"job": "node-compute", "node": node, "instance": instance or node + ":9100"}
    if url is not None:
        labels["url"] = url
    return metric(name if name == "up" else "faas_serving_probe_" + name, **labels)


def healthy():
    return {
        intent("inventory_configured"): "1" + STEPS,
        intent("expected_pairs"): "1" + STEPS,
        intent("expected", probe_node="fsn-2", url=URL): "1" + STEPS,
        intent("expected_first_byte_seconds", probe_node="fsn-2"): "2" + STEPS,
        intent("expected_max_age_seconds", probe_node="fsn-2"): "900" + STEPS,
        observed("up"): "1" + STEPS,
        observed("last_run_timestamp_seconds"): "0+60x60",
        observed("content_check_enabled"): "1" + STEPS,
        observed("first_byte_budget_seconds"): "2" + STEPS,
        observed("success", url=URL): "1" + STEPS,
    }


def cases():
    """Cases define expected outcomes independently of the production queries."""
    base = healthy()
    yield "healthy", base, [("fsn-2", URL, 0)], {"fsn-2": 1}, 1

    missing = {k: v for k, v in base.items() if 'job="node-compute"' not in k}
    yield "never installed", missing, [("fsn-2", URL, 1)], {"fsn-2": 0}, 1

    wrong_node = {k.replace('"fsn-2"', '"fsn-3"').replace('"fsn-2:9100"', '"fsn-3:9100"')
                  if 'job="node-compute"' in k else k: v for k, v in base.items()}
    yield "a healthy different node cannot satisfy the inventory", wrong_node, [("fsn-2", URL, 1)], {"fsn-2": 0}, 1

    wrong_url = dict(base)
    wrong_url[observed("success", url=OTHER_URL)] = wrong_url.pop(observed("success", url=URL))
    yield "a healthy different URL cannot satisfy the inventory", wrong_url, [("fsn-2", URL, 1)], {"fsn-2": 0}, 1

    vanished = dict(base)
    vanished[observed("success", url=URL)] = "1x10 stale"
    yield "deleted result file remains missing after series go stale", vanished, [("fsn-2", URL, 1)], {"fsn-2": 0}, 1

    frozen = dict(base)
    frozen[observed("last_run_timestamp_seconds")] = "0" + STEPS
    yield "a frozen textfile is stale despite successful scrapes", frozen, [("fsn-2", URL, 2)], {"fsn-2": 0}, 1

    future = dict(base)
    future[observed("last_run_timestamp_seconds")] = "3600+60x60"
    yield "a future clock cannot establish freshness", future, [("fsn-2", URL, 2)], {"fsn-2": 0}, 1

    down = dict(base)
    down[observed("up")] = "0" + STEPS
    yield "a failed node scrape cannot establish coverage", down, [("fsn-2", URL, 2)], {"fsn-2": 0}, 1

    disabled = dict(base)
    disabled[observed("content_check_enabled")] = "0" + STEPS
    yield "disabled content validation", disabled, [("fsn-2", URL, 4)], {"fsn-2": 0}, 1

    budget = dict(base)
    budget[observed("first_byte_budget_seconds")] = "3" + STEPS
    yield "unexpected latency budget", budget, [("fsn-2", URL, 5)], {"fsn-2": 0}, 1

    no_config = dict(base)
    no_config.pop(observed("content_check_enabled"))
    yield "missing validation metadata is invalid evidence", no_config, [("fsn-2", URL, 3)], {"fsn-2": 0}, 1

    duplicate = dict(base)
    duplicate[observed("success", url=URL, instance="duplicate:9100")] = "1" + STEPS
    yield "duplicate node URL results cannot hide a fault", duplicate, [("fsn-2", URL, 3)], {"fsn-2": 0}, 1

    wrong_target = {k.replace('"fsn-2:9100"', '"other:9100"')
                    if 'job="node-compute"' in k and not k.startswith("up{") else k: v for k, v in base.items()}
    yield "up from one target cannot validate another target's metrics", wrong_target, [("fsn-2", URL, 2)], {"fsn-2": 0}, 1

    mixed_config = dict(base)
    mixed_config[observed("content_check_enabled", instance="other:9100")] = mixed_config.pop(observed("content_check_enabled"))
    yield "validation metadata must belong to the discovered target", mixed_config, [("fsn-2", URL, 3)], {"fsn-2": 0}, 1

    non_boolean = dict(base)
    non_boolean[observed("success", url=URL)] = "0.5" + STEPS
    yield "non-boolean results cannot appear healthy", non_boolean, [("fsn-2", URL, 3)], {"fsn-2": 0}, 1

    failed = dict(base)
    failed[observed("success", url=URL)] = "0" + STEPS
    yield "a failed response is visible separately from missing coverage", failed, [("fsn-2", URL, 6)], {"fsn-2": 0}, 1

    two_urls = dict(base)
    two_urls[intent("expected_pairs")] = "2" + STEPS
    two_urls[intent("expected", probe_node="fsn-2", url=OTHER_URL)] = "1" + STEPS
    yield "one healthy URL cannot hide another missing URL", two_urls, [("fsn-2", URL, 0), ("fsn-2", OTHER_URL, 1)], {"fsn-2": 0.5}, 1

    incomplete_intent = dict(base)
    incomplete_intent[intent("expected_pairs")] = "2" + STEPS
    yield "partial inventory cannot produce a healthy verdict", incomplete_intent, [], {}, None

    no_intent = {k: v for k, v in base.items() if 'job="node"' not in k}
    yield "inventory source disappeared", no_intent, [], {}, None

    unconfigured = {intent("inventory_configured"): "0" + STEPS, intent("expected_pairs"): "0" + STEPS}
    yield "unconfigured inventory is unknown rather than healthy", unconfigured, [], {}, 0

    empty = {intent("inventory_configured"): "1" + STEPS, intent("expected_pairs"): "0" + STEPS}
    yield "explicitly empty inventory expects no probes", empty, [], {}, 1

    recovered = dict(base)
    recovered[observed("content_check_enabled")] = "0x20 1x40"
    yield "coverage recovers when validation is restored", recovered, [("fsn-2", URL, 0)], {"fsn-2": 1}, 1


@unittest.skipUnless(PROMTOOL, "promtool is required for real coverage evaluation")
class ServingProbeCoverageTest(unittest.TestCase):
    def invoke(self, data):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "coverage.test.yml"
            path.write_text(json.dumps(data))
            result = subprocess.run([PROMTOOL, "test", "rules", str(path)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_node_url_membership_freshness_and_configuration(self):
        tests = []
        for name, series, states, ratios, inventory_status in cases():
            samples = [{"labels": metric("faas_serving_probe:coverage_state", probe_node=node, url=url), "value": state}
                       for node, url, state in states]
            ratio_samples = [{"labels": metric("faas_serving_probe:coverage_ratio", probe_node=node), "value": ratio}
                             for node, ratio in ratios.items()]
            inventory_samples = [] if inventory_status is None else [
                {"labels": "faas_serving_probe:inventory_status{}", "value": inventory_status}]
            tests.append({
                "name": name,
                "interval": "1m",
                "input_series": [{"series": key, "values": value} for key, value in series.items()],
                "promql_expr_test": [
                    {"expr": "faas_serving_probe:coverage_state", "eval_time": "30m", "exp_samples": samples},
                    {"expr": "faas_serving_probe:coverage_ratio", "eval_time": "30m", "exp_samples": ratio_samples},
                    {"expr": "faas_serving_probe:inventory_status", "eval_time": "30m", "exp_samples": inventory_samples},
                ],
            })
        self.invoke({"rule_files": [str(ROOT / "deploy/ansible/roles/prometheus/files/faas.rules.yml")],
                     "evaluation_interval": "1m", "tests": tests})

    def test_dashboard_cannot_show_old_recordings_as_current_health(self):
        dashboard = json.loads((ROOT / "deploy/grafana/faas-fleet.json").read_text())
        expressions = {p["id"]: p["targets"][0] for p in dashboard["panels"] if p["id"] in (440, 441, 442, 443)}
        self.assertEqual(len(expressions), 4)
        self.assertTrue(all(target.get("instant") for target in expressions.values()))
        inputs = [
            ("faas_serving_probe:inventory_status{}", 1),
            (intent("expected_pairs"), 3),
            (metric("faas_serving_probe:coverage_ratio", probe_node="fsn-2"), 0.5),
            (metric("faas_serving_probe:coverage_state", probe_node="fsn-2", url=URL), 0),
        ]
        expected = {440: [inputs[0]], 441: [inputs[1]], 442: [inputs[2]], 443: [inputs[3]]}
        checks = []
        for panel_id, target in expressions.items():
            for at in ("1m", "3m"):
                # At 3m Prometheus still has the last sample in its 5m
                # lookback, but the dashboard must reject it as stale.
                samples = [] if at == "3m" else [{"labels": labels, "value": value}
                                                  for labels, value in expected[panel_id]]
                checks.append({"expr": target["expr"], "eval_time": at, "exp_samples": samples})
        self.invoke({"evaluation_interval": "1m", "tests": [{
            "name": "current versus stale fleet dashboard observations", "interval": "1m",
            "input_series": [{"series": labels, "values": str(value)} for labels, value in inputs],
            "promql_expr_test": checks,
        }]})


if __name__ == "__main__":
    unittest.main()
