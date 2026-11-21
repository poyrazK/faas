#!/usr/bin/env python3
"""Evaluate serving-probe coverage against the real Prometheus rule engine."""

import json
import os
from pathlib import Path
import re
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


NOTIFICATION_FIELDS = ("enabled", "attempted", "last_attempt_success", "last_attempt_failure_code",
                       "last_attempt_timestamp_seconds", "last_success_timestamp_seconds")


def notification(name, channel="webhook", **labels):
    return metric("faas_serving_probe_notification_" + name, job="node-compute", node="fsn-2",
                  instance=labels.pop("instance", "fsn-2:9100"), channel=channel, **labels)


def notification_failure():
    series = healthy()
    for field, value in zip(NOTIFICATION_FIELDS, (1, 1, 0, 4, 300, 120)):
        series[notification(field)] = str(value) + STEPS
    series[notification("enabled", "heartbeat")] = "0" + STEPS
    series[notification("attempted", "heartbeat")] = "0" + STEPS
    return series


def notification_cases():
    """Expected state/code pairs, independent of the shipped expressions."""
    base = notification_failure()
    yield "confirmed failure", base, {"webhook": 3, "heartbeat": 0}, 4
    skipped = dict(base, **{notification("attempted"): "0" + STEPS})
    yield "a skip preserves the last completed failure", skipped, {"webhook": 3, "heartbeat": 0}, 4
    equal_clocks = dict(base, **{notification("last_success_timestamp_seconds"): "300" + STEPS})
    yield "equal clocks retain the reported last failure", equal_clocks, {"webhook": 3, "heartbeat": 0}, 4
    heartbeat_failure = {k.replace('channel="webhook"', 'channel="temporary"')
                         .replace('channel="heartbeat"', 'channel="webhook"')
                         .replace('channel="temporary"', 'channel="heartbeat"'): v for k, v in base.items()}
    yield "a disabled webhook cannot hide a failed heartbeat", heartbeat_failure, {"webhook": 0, "heartbeat": 3}, 4
    both_failed = dict(base)
    for field, value in zip(NOTIFICATION_FIELDS, (1, 0, 0, 4, 300, 120)):
        both_failed[notification(field, "heartbeat")] = str(value) + STEPS
    yield "each failed channel has its own alert", both_failed, {"webhook": 3, "heartbeat": 3}, 4

    for code in (1, 2, 3, 5, 8):
        series = dict(base, **{notification("last_attempt_failure_code"): str(code) + STEPS})
        yield f"accepted failure code {code}", series, {"webhook": 3, "heartbeat": 0}, code

    for mode in ("success", "quiet success", "disabled", "never attempted", "unknown history"):
        series = dict(base)
        if mode in ("success", "quiet success"):
            series[notification("last_attempt_success")] = "1" + STEPS
            series[notification("last_attempt_failure_code")] = "0" + STEPS
            series[notification("last_success_timestamp_seconds")] = "300" + STEPS
            series[notification("attempted")] = ("0" if mode == "quiet success" else "1") + STEPS
            state = 2
        else:
            for name in NOTIFICATION_FIELDS[2:]:
                series.pop(notification(name))
            series[notification("attempted")] = "0" + STEPS
            state = 1
            if mode == "disabled":
                series[notification("enabled")] = "0" + STEPS
                state = 0
            elif mode == "unknown history":
                series[notification("attempted")] = "1" + STEPS
        yield mode, series, {"webhook": state, "heartbeat": 0}, None

    for field in NOTIFICATION_FIELDS:
        for defect in ("missing", "duplicate", "wrong target", "old scrape"):
            series = dict(base)
            key = notification(field)
            if defect == "missing":
                series.pop(key)
            elif defect == "duplicate":
                series[notification(field, copy="duplicate")] = series[key]
            elif defect == "wrong target":
                series[notification(field, instance="other:9100")] = series.pop(key)
            else:
                series[key] = series[key].removesuffix(STEPS) + "x27"
            still_known = field == "last_success_timestamp_seconds" and defect == "missing"
            yield f"{field}: {defect}", series, {"webhook": 3 if still_known else 1, "heartbeat": 0}, 4 if still_known else None

    for field, value in (("enabled", "0.5"), ("attempted", "2"), ("last_attempt_success", "0.5"),
                         ("last_attempt_failure_code", "0"), ("last_attempt_failure_code", "4.5"),
                         ("last_attempt_failure_code", "6"), ("last_attempt_failure_code", "7"),
                         ("last_attempt_failure_code", "9"), ("last_attempt_failure_code", "NaN"),
                         ("last_attempt_timestamp_seconds", "0"), ("last_attempt_timestamp_seconds", "Inf"),
                         ("last_attempt_timestamp_seconds", "1831"), ("last_success_timestamp_seconds", "0"),
                         ("last_success_timestamp_seconds", "NaN"), ("last_success_timestamp_seconds", "301")):
        values = " ".join([value] * 61) if value in ("NaN", "Inf") else value + STEPS
        series = dict(base, **{notification(field): values})
        yield f"invalid {field}: {value}", series, {"webhook": 1, "heartbeat": 0}, None

    for value, state in (("1830", 3), ("1831", 1)):
        series = dict(base, **{notification("last_attempt_timestamp_seconds"): value + STEPS})
        yield f"completion skew boundary {value}", series, {"webhook": state, "heartbeat": 0}, 4 if state == 3 else None

    for mode in ("missing success clock", "success without matching clock", "success with failure code"):
        series = dict(base, **{notification("last_attempt_success"): "1" + STEPS})
        series[notification("last_attempt_failure_code")] = ("4" if mode == "success with failure code" else "0") + STEPS
        if mode == "missing success clock":
            series.pop(notification("last_success_timestamp_seconds"))
        yield mode, series, {"webhook": 1, "heartbeat": 0}, None

    for mode in ("disabled but attempted", "disabled with history"):
        series = dict(base, **{notification("enabled"): "0" + STEPS, notification("attempted"): "0" + STEPS})
        if mode == "disabled but attempted":
            for field in NOTIFICATION_FIELDS[2:]:
                series.pop(notification(field))
            series[notification("attempted")] = "1" + STEPS
        yield mode, series, {"webhook": 1, "heartbeat": 0}, None

    for mode in ("frozen run", "future run", "old run scrape", "old up scrape", "down scrape", "duplicate target",
                 "duplicate up labels", "duplicate run", "wrong run target", "wrong node identity",
                 "invalid policy", "duplicate policy", "old policy scrape", "run clock newer than its own scrape"):
        series = dict(base)
        if mode == "frozen run":
            series[observed("last_run_timestamp_seconds")] = "0" + STEPS
        elif mode == "future run":
            series[observed("last_run_timestamp_seconds")] = "3600+60x60"
        elif mode == "old run scrape":
            series[observed("last_run_timestamp_seconds")] = "0+60x27"
        elif mode == "old up scrape":
            series[observed("up")] = "1x27"
        elif mode == "down scrape":
            series[observed("up")] = "0" + STEPS
        elif mode == "duplicate target":
            series[observed("up", instance="other:9100")] = "1" + STEPS
        elif mode == "duplicate up labels":
            series[observed("up")[:-1] + ',copy="duplicate"}'] = "1" + STEPS
        elif mode == "duplicate run":
            series[observed("last_run_timestamp_seconds")[:-1] + ',copy="duplicate"}'] = "0+60x60"
        elif mode == "wrong run target":
            series[observed("last_run_timestamp_seconds", instance="other:9100")] = series.pop(observed("last_run_timestamp_seconds"))
        elif mode == "wrong node identity":
            series[observed("up")[:-1] + ',node_id="different-node"}'] = series.pop(observed("up"))
        elif mode == "invalid policy":
            series[intent("expected_max_age_seconds", probe_node="fsn-2")] = " ".join(["Inf"] * 61)
        elif mode == "duplicate policy":
            series[intent("expected_max_age_seconds", probe_node="fsn-2", copy="duplicate")] = "900" + STEPS
        elif mode == "run clock newer than its own scrape":
            series = {k: v.removesuffix(STEPS) + "x28" if k.startswith("faas_serving_probe_notification_") else v
                      for k, v in series.items()}
            series[observed("up")] = "1x28"
            series[observed("last_run_timestamp_seconds")] = "1800x28"
        else:
            series[intent("expected_max_age_seconds", probe_node="fsn-2")] = "900x27"
        yield mode, series, {"webhook": 1, "heartbeat": 1}, None

    no_metadata = {k: v for k, v in base.items() if not k.startswith("faas_serving_probe_notification_")}
    yield "older producer has unknown notification health", no_metadata, {"webhook": 1, "heartbeat": 1}, None

    hidden_labels = {k[:-1] + ',provider="private-provider-token",url="https://private.example/token"}'
                     if k.startswith("faas_serving_probe_notification_") else k: v for k, v in base.items()}
    yield "unreviewed labels never reach normalized output", hidden_labels, {"webhook": 3, "heartbeat": 0}, 4

    rotated = dict(base)
    for name in NOTIFICATION_FIELDS[2:]:
        rotated[notification(name)] = rotated[notification(name)].removesuffix(STEPS) + "x20 stale"
    rotated[notification("attempted")] = "0" + STEPS
    yield "credential rotation removes completed history", rotated, {"webhook": 1, "heartbeat": 0}, None
    disabled_after_failure = dict(rotated)
    disabled_after_failure[notification("enabled")] = "1x20 0x39"
    disabled_after_failure[notification("attempted")] = "1x20 0x39"
    yield "disabling removes an existing warning without confirming recovery", disabled_after_failure, {"webhook": 0, "heartbeat": 0}, None

    wrong_channel = {k.replace('channel="webhook"', 'channel="unreviewed"'): v for k, v in base.items()}
    yield "unsupported channel cannot qualify a webhook", wrong_channel, {"webhook": 1, "heartbeat": 0}, None

    no_intent = {k: v for k, v in base.items() if 'job="node"' not in k}
    yield "unavailable intent cannot qualify a delivery alert", no_intent, {}, None

    no_validation = {k: v for k, v in base.items() if not k.startswith(("faas_serving_probe_content_check_enabled", "faas_serving_probe_first_byte_budget_seconds"))}
    yield "notification evidence is independent of serving validation", no_validation, {"webhook": 3, "heartbeat": 0}, 4


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
    def invoke(self, data, serving_only=False):
        with tempfile.TemporaryDirectory() as directory:
            if serving_only:
                # Evaluate the exact production group without unrelated alert
                # families; all its dependencies are defined earlier in it.
                source = (ROOT / "deploy/ansible/roles/prometheus/files/faas.rules.yml").read_text()
                group = re.search(r"(?ms)^  - name: faas_serving_probe_coverage\n.*?(?=^  - name:|\Z)", source)
                self.assertIsNotNone(group)
                rules = Path(directory) / "serving.rules.yml"
                rules.write_text("groups:\n" + group[0])
                data = dict(data, rule_files=[str(rules)])
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

    def test_diagnostics_belong_to_one_current_discovered_result(self):
        values = {"failure_code": 0, "first_failure_code": 5, "attempts": 2, "http_status": 200}
        tests = []
        for mode in ("recovered retry", "old probe", "wrong target", "duplicate", "stale field", "frozen run"):
            series = healthy()
            wanted = dict(values)
            if mode != "old probe":
                for name, value in values.items():
                    series[observed(name, url=URL)] = str(value) + STEPS
            else:
                wanted = {}
            if mode == "wrong target":
                series[observed("attempts", url=URL, instance="other:9100")] = series.pop(observed("attempts", url=URL))
                wanted.pop("attempts")
            elif mode == "duplicate":
                duplicate = observed("attempts", url=URL)[:-1] + ',copy="duplicate"}'
                series[duplicate] = "2" + STEPS
                wanted.pop("attempts")
            elif mode == "stale field":
                series[observed("first_failure_code", url=URL)] = "5x27"
                wanted.pop("first_failure_code")
            elif mode == "frozen run":
                series[observed("last_run_timestamp_seconds")] = "0" + STEPS
                wanted = {}
            tests.append({
                "name": "diagnostics: " + mode, "interval": "1m",
                "input_series": [{"series": key, "values": value} for key, value in series.items()],
                "promql_expr_test": [{
                    "expr": "{__name__=~\"faas_serving_probe:(failure_code|first_failure_code|attempts|http_status)\"}",
                    "eval_time": "30m",
                    "exp_samples": [{"labels": metric("faas_serving_probe:" + name, probe_node="fsn-2", url=URL), "value": value}
                                    for name, value in wanted.items()],
                }],
            })
        self.invoke({"rule_files": [str(ROOT / "deploy/ansible/roles/prometheus/files/faas.rules.yml")],
                     "evaluation_interval": "1m", "tests": tests})

    def test_notification_state_and_alert_reject_unusable_evidence(self):
        tests = []
        for name, series, states, code in notification_cases():
            expected_alerts = [{
                "labels": metric("", alertname="FaasServingProbeNotificationFailed",
                                 probe_node="fsn-2", channel=channel, severity="warn", component="node",
                                 family="serving_probe_notifications", public_status="internal"), "value": 1}
                for channel, state in states.items() if state == 3]
            expected_codes = [{"labels": metric("faas_serving_probe_notification:last_attempt_failure_code",
                                               probe_node="fsn-2", channel=channel), "value": code if state == 3 else 0}
                              for channel, state in states.items() if state in (2, 3)]
            tests.append({
                "name": "notification: " + name, "interval": "1m",
                "input_series": [{"series": key, "values": value} for key, value in series.items()],
                "promql_expr_test": [
                    {"expr": "faas_serving_probe_notification:state", "eval_time": "30m",
                     "exp_samples": [{"labels": metric("faas_serving_probe_notification:state", probe_node="fsn-2", channel=channel), "value": state}
                                     for channel, state in states.items()]},
                    # The checked-in fixture tests the five-minute hold. This
                    # matrix checks eligibility, including a just-valid clock
                    # whose alert is still pending at the evaluation time.
                    {"expr": "count without (alertstate) (ALERTS{alertname=\"FaasServingProbeNotificationFailed\"})",
                     "eval_time": "30m", "exp_samples": expected_alerts},
                    {"expr": "faas_serving_probe_notification:last_attempt_failure_code", "eval_time": "30m",
                     "exp_samples": expected_codes},
                ],
            })
        self.invoke({"evaluation_interval": "1m", "tests": tests}, serving_only=True)

    def test_notification_warning_hold_recovery_rotation_and_disable_fixture(self):
        fixture = ROOT / "pkg/promqlrules/testdata/serving_probe_notifications.test.yml"
        result = subprocess.run([PROMTOOL, "test", "rules", str(fixture)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_notification_evidence_distinguishes_gaps_from_no_history(self):
        expected = {
            "confirmed failure": {"webhook": 0, "heartbeat": 0},
            "quiet success": {"webhook": 0, "heartbeat": 0},
            "disabled": {"webhook": 0, "heartbeat": 0},
            "never attempted": {"webhook": 1, "heartbeat": 0},
            "unknown history": {"webhook": 2, "heartbeat": 0},
            "missing success clock": {"webhook": 2, "heartbeat": 0},
            "success without matching clock": {"webhook": 3, "heartbeat": 0},
            "disabled but attempted": {"webhook": 2, "heartbeat": 0},
            "disabled with history": {"webhook": 3, "heartbeat": 0},
            "older producer has unknown notification health": {"webhook": 2, "heartbeat": 2},
            "credential rotation removes completed history": {"webhook": 1, "heartbeat": 0},
            "disabling removes an existing warning without confirming recovery": {"webhook": 0, "heartbeat": 0},
            "unsupported channel cannot qualify a webhook": {"webhook": 2, "heartbeat": 0},
            "unreviewed labels never reach normalized output": {"webhook": 0, "heartbeat": 0},
            "unavailable intent cannot qualify a delivery alert": {},
        }
        for field in NOTIFICATION_FIELDS:
            for defect in ("missing", "duplicate", "wrong target", "old scrape"):
                code = (0 if field == "last_success_timestamp_seconds" else 2) if defect == "missing" else 3
                expected[f"{field}: {defect}"] = {"webhook": code, "heartbeat": 0}
        for name, _, _, _ in notification_cases():
            if name.startswith("invalid "):
                expected[name] = {"webhook": 3, "heartbeat": 0}
        for name in ("frozen run", "future run", "old run scrape", "old up scrape", "down scrape", "duplicate target",
                     "duplicate up labels", "duplicate run", "wrong run target", "wrong node identity",
                     "invalid policy", "duplicate policy", "old policy scrape", "run clock newer than its own scrape"):
            expected[name] = {"webhook": 4, "heartbeat": 4}
        tests = []
        for name, series, _, _ in notification_cases():
            if name not in expected:
                continue
            codes = expected[name]
            tests.append({
                "name": "notification evidence: " + name, "interval": "1m",
                "input_series": [{"series": key, "values": value} for key, value in series.items()],
                "promql_expr_test": [
                    {"expr": "faas_serving_probe_notification:evidence_status", "eval_time": "30m",
                     "exp_samples": [{"labels": metric("faas_serving_probe_notification:evidence_status", probe_node="fsn-2", channel=channel), "value": code}
                                     for channel, code in codes.items()]},
                    {"expr": "count without (alertstate) (ALERTS{alertname=\"FaasServingProbeNotificationTelemetryDegraded\"})",
                     "eval_time": "30m", "exp_samples": [
                         {"labels": metric("", alertname="FaasServingProbeNotificationTelemetryDegraded", probe_node="fsn-2", channel=channel,
                                           severity="warn", component="node", family="serving_probe_notifications", public_status="internal"), "value": 1}
                         for channel, code in codes.items() if code in (2, 3)]},
                ],
            })
        self.assertEqual(len(tests), len(expected))
        self.invoke({"evaluation_interval": "1m", "tests": tests}, serving_only=True)

    def test_notification_telemetry_warning_lifecycle_fixture(self):
        fixture = ROOT / "pkg/promqlrules/testdata/serving_probe_notification_evidence.test.yml"
        result = subprocess.run([PROMTOOL, "test", "rules", str(fixture)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
