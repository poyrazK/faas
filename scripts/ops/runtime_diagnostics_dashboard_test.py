#!/usr/bin/env python3
"""Evaluate the provisioned runtime dashboard's PromQL with promtool."""

from functools import partial
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
PROMTOOL = os.environ.get("PROMTOOL", "promtool")
METRIC_PREFIX = "apid_operator_runtime_"
COVERAGE_PANEL = 12
REPORT_AGE_PANEL = 13
INCOMPLETE_GROUPS_PANEL = 14
INCOMPLETE_RATE_PANEL = 15
CORE_GAPS_PANEL = 16
WARNING_TIMELINE_PANEL = 17
LAST_COLLECTION_PANEL = 18
SOURCE_FAILURES_PANEL = 19


def series(name, values, instance="apid-a:9101", job="apid", **extra_labels):
    labels = {"job": job, "instance": instance, **extra_labels}
    return {"series": METRIC_PREFIX + name + selector(**labels), "values": values}


def scrape(values, instance="apid-a:9101", job="apid"):
    return {"series": "up" + selector(job=job, instance=instance), "values": values}


def alert_series(alertname, state, values, **labels):
    return {"series": "ALERTS" + selector(alertname=alertname, alertstate=state, **labels), "values": values}


def build_info(sha, values, build_time="build-1", **labels):
    return {"series": "apid_daemon_build_info" + selector(daemon="apid", git_sha=sha, build_time=build_time, **labels),
            "values": values}


def uptime(values, **labels):
    return {"series": "apid_daemon_uptime_seconds" + selector(daemon="apid", **labels), "values": values}


def selector(**labels):
    return "{" + ",".join(f"{key}={json.dumps(value)}" for key, value in labels.items()) + "}"


def sample(value, *, metric_name="", **labels):
    return {"labels": metric_name + selector(**labels), "value": value}


class RuntimeDiagnosticsDashboardTest(unittest.TestCase):
    def test_queries_on_idle_saturated_and_restarted_collectors(self):
        dashboard = (ROOT / "deploy/grafana/runtime-diagnostics.json").read_bytes()
        provisioned = ROOT / "deploy/ansible/roles/grafana/files/runtime-diagnostics.json"
        self.assertEqual(dashboard, provisioned.read_bytes())
        dashboard = json.loads(dashboard)
        panels = {panel["id"]: panel for panel in dashboard["panels"]}
        [variable] = [v for v in dashboard["templating"]["list"] if v["name"] == "apid_instance"]
        self.assertTrue(variable["includeAll"])
        self.assertEqual(variable["current"]["value"], "$__all")
        self.assertEqual(variable["query"]["query"], 'label_values(up{job="apid"}, instance)')
        [evidence_group] = [v for v in dashboard["templating"]["list"] if v["name"] == "evidence_group"]
        self.assertTrue(evidence_group["includeAll"])
        self.assertEqual(evidence_group["current"]["value"], "$__all")
        self.assertEqual(evidence_group["query"]["query"],
                         'label_values(apid_operator_runtime_query_attempts_total{job="apid",instance=~"$apid_instance"}, group)')
        [revision_annotation] = [a for a in dashboard["annotations"]["list"] if a["name"] == "apid revision changes"]
        self.assertTrue(revision_annotation["enable"])
        self.assertTrue(revision_annotation["useValueForTime"])
        self.assertEqual(revision_annotation["step"], "15s")
        self.assertIn("{{instance}}", revision_annotation["titleFormat"])
        self.assertIn("{{revision}}", revision_annotation["titleFormat"])
        [restart_annotation] = [a for a in dashboard["annotations"]["list"] if a["name"] == "apid restarts"]
        self.assertTrue(restart_annotation["enable"])
        self.assertTrue(restart_annotation["useValueForTime"])
        self.assertEqual(restart_annotation["step"], "15s")
        self.assertIn("{{instance}}", restart_annotation["titleFormat"])
        [warning_states] = panels[WARNING_TIMELINE_PANEL]["fieldConfig"]["defaults"]["mappings"]
        self.assertEqual(warning_states["options"]["-3"]["text"], "Insufficient evaluations")
        for panel in panels.values():
            for target in panel["targets"]:
                expr = target["expr"]
                self.assertIn('job="apid"', expr, panel["title"])
                self.assertEqual(expr.count('job="apid"'), expr.count('job="apid",instance=~"$apid_instance"'), panel["title"])
                group_scoped = (panel["id"] in (5, 6, 8, INCOMPLETE_GROUPS_PANEL, INCOMPLETE_RATE_PANEL, CORE_GAPS_PANEL, SOURCE_FAILURES_PANEL)
                                or (panel["id"] == WARNING_TIMELINE_PANEL and target["refId"] == "C"))
                self.assertEqual("$evidence_group" in expr, group_scoped, panel["title"])

        def interpolate(expr, instance, group=None):
            pattern = variable["allValue"] if instance is None else re.escape(instance)
            group_pattern = evidence_group["allValue"] if group is None else re.escape(group)
            return expr.replace('"$apid_instance"', json.dumps(pattern)).replace("$evidence_group", json.dumps(group_pattern)[1:-1])

        def check(panel_id, *expected, eval_time="5m", instance=None, ref_id="A", group=None):
            expr = next(target["expr"] for target in panels[panel_id]["targets"] if target["refId"] == ref_id)
            expr = interpolate(expr, instance, group)
            if panel_id in (4, 6, 7, 9):
                # Promtool 2.54 compares floats exactly. Check histogram
                # interpolation to four decimals without depending on its last bit.
                expr = f"round(({expr}) * 10000) / 10000"
            return {"expr": expr, "eval_time": eval_time, "exp_samples": expected}

        def annotation_at(annotation, eval_time, *expected, instance=None):
            return {"expr": interpolate(annotation["expr"], instance),
                    "eval_time": eval_time, "exp_samples": expected}

        revision_at = partial(annotation_at, revision_annotation)
        restart_at = partial(annotation_at, restart_annotation)

        def case(name, inputs, checks, interval="1m"):
            return {"name": name, "interval": interval, "input_series": inputs, "promql_expr_test": checks}

        idle = [
            scrape("1x5"),
            series("active_collections", "0x5"),
            series("refused_collections_total", "0x5"),
            series("reports_total", "0x5", outcome="complete"),
            series("incomplete_reports_total", "0x5", group="readiness"),
            series("collection_duration_seconds_bucket", "0x5", le="1"),
            series("collection_duration_seconds_bucket", "0x5", le="+Inf"),
            series("query_attempts_total", "0x5", group="identity", outcome="source_error"),
            series("query_duration_seconds_bucket", "0x5", group="identity", le="0.25"),
            series("query_duration_seconds_bucket", "0x5", group="identity", le="+Inf"),
            series("response_bytes_bucket", "0x5", le="4096"),
            series("response_bytes_bucket", "0x5", le="+Inf"),
            series("preview_omitted_items_total", "0x5", group="identity"),
            series("phase_duration_seconds_bucket", "0x5", phase="inventory", le="1"),
            series("phase_duration_seconds_bucket", "0x5", phase="inventory", le="+Inf"),
            series("inventory_attempts_total", "0x5", outcome="success"),
            series("collection_deadlines_total", "0x5", phase="inventory", origin="caller_deadline"),
        ]
        saturated_and_restarted = [
            scrape("1x5"),
            scrape("1x5", instance="apid-b:9101"),
            series("active_collections", "0 0 1 1 2 2"),
            series("active_collections", "0x5", instance="apid-b:9101"),
            series("refused_collections_total", "0 0 1 0 1 2"),
            series("refused_collections_total", "4x5", instance="apid-b:9101"),
            series("reports_total", "0 0 1 0 1 2", outcome="incomplete"),
            series("reports_total", "0 0 1 2 3 4", instance="apid-b:9101", outcome="incomplete"),
            series("reports_total", "0 0 1 1 1 1", outcome="deadline_exceeded"),
            series("incomplete_reports_total", "0 0 1 0 1 2", group="readiness"),
            series("incomplete_reports_total", "0 0 1 2 3 4", instance="apid-b:9101", group="readiness"),
            series("incomplete_reports_total", "0 0 1 1 1 1", group="loop_health"),
            series("incomplete_reports_total", "0x5", group="identity"),
            series("collection_duration_seconds_bucket", "0 0 5 10 15 20", le="1"),
            series("collection_duration_seconds_bucket", "0 0 5 10 15 20", le="+Inf"),
            series("query_attempts_total", "0 0 1 0 1 2", group="identity", outcome="deadline_exceeded"),
            series("query_attempts_total", "0 0 1 2 3 4", group="builder_health", outcome="source_error"),
            series("query_attempts_total", "0 0 1 2 3 4", group="builder_health", outcome="success"),
            series("query_duration_seconds_bucket", "0 0 5 10 15 20", group="identity", le="0.25"),
            series("query_duration_seconds_bucket", "0 0 5 10 15 20", group="identity", le="+Inf"),
            series("response_bytes_bucket", "0x5", le="32768"),
            series("response_bytes_bucket", "0 0 5 10 15 20", le="65536"),
            series("response_bytes_bucket", "0 0 5 10 15 20", le="+Inf"),
            series("preview_omitted_items_total", "0 0 8 0 6 7", group="identity"),
            series("preview_omitted_items_total", "0x5", group="builder_health"),
            series("phase_duration_seconds_bucket", "0 0 1 0 1 2", phase="inventory", le="1"),
            series("phase_duration_seconds_bucket", "0 0 1 0 1 2", phase="inventory", le="+Inf"),
            series("phase_duration_seconds_bucket", "0 0 5 10 15 20", instance="apid-b:9101", phase="core_evidence", le="0.25"),
            series("phase_duration_seconds_bucket", "0 0 5 10 15 20", instance="apid-b:9101", phase="core_evidence", le="+Inf"),
            series("inventory_attempts_total", "0 0 1 0 1 2", outcome="deadline_exceeded"),
            series("inventory_attempts_total", "0 0 1 2 3 4", instance="apid-b:9101", outcome="source_error"),
            series("collection_deadlines_total", "0 0 1 0 1 2", phase="inventory", origin="caller_deadline"),
            series("collection_deadlines_total", "0 0 1 2 3 4", phase="core_evidence", origin="collection_budget"),
            series("collection_deadlines_total", "0 0 1 1 1 1", instance="apid-b:9101", phase="inventory", origin="collection_budget"),
        ]
        a = {"job": "apid", "instance": "apid-a:9101"}
        b = {"job": "apid", "instance": "apid-b:9101"}
        c = {"job": "apid", "instance": "apid-c:9101"}
        active = {"metric_name": METRIC_PREFIX + "active_collections"}
        no_targets = sample(3, job="apid", instance="Prometheus")
        selected = {"job": "apid", "instance": "127.0.0.1:9101"}
        # An unescaped dot in the selected instance would also match this replica.
        other = {"job": "apid", "instance": "127a0b0c1:9101"}
        alerts = (
            ("FaasRuntimeCollectorCapacityRefusals", selected),
            ("FaasRuntimeCollectorDeadlines", selected),
            ("FaasRuntimeCollectorIncompleteCoreEvidence", dict(selected, group="readiness")),
        )
        # Capacity and deadline state, readiness state, state of other core rows.
        warning_transitions = [
            (0, -3, -1), (1, 1, -1), (2, 2, -1), (0, -3, -1),
            (-2, -2, -2), (0, -3, -1), (-1, -1, -1),
        ]
        during_upgrade = [
            scrape("1x5", **a),
            scrape("1x5", **b),
            scrape("1x4 0", **c),
            series("active_collections", "0x5", **a),
            series("active_collections", "_ _ _ _ _ 1", **b),
            # At 5m this old sample still falls inside the lookback window.
            series("active_collections", "0x4 _", **c),
            series("active_collections", "2x5", instance=b["instance"], job="other"),
        ]
        fixtures = {
            # Promtool loads samples on this cadence, including for annotation checks.
            "evaluation_interval": "15s",
            "tests": [
                case("metrics unavailable from apid", [
                    scrape("1x5", job="other"),
                    series("active_collections", "2x5", job="other"),
                    series("refused_collections_total", "0+1x5", job="other"),
                    series("incomplete_reports_total", "0+1x5", job="other", group="readiness"),
                ], [
                    check(COVERAGE_PANEL, no_targets),
                    *[check(i) for i in panels if i != COVERAGE_PANEL],
                    check(INCOMPLETE_RATE_PANEL, ref_id="B"),
                    check(WARNING_TIMELINE_PANEL, ref_id="B"),
                    check(WARNING_TIMELINE_PANEL, ref_id="C"),
                ]),
                case("warning timeline distinguishes active warnings and telemetry gaps", [
                    scrape("1 1 1 1 0 1 1", **selected),
                    scrape("1x6", **other),
                    series("refused_collections_total", "0x3 _ 0 stale", **selected),
                    series("reports_total", "0x3 _ 0 stale", outcome="deadline_exceeded", **selected),
                    series("group_evaluations_total", "0x3 _ 0 stale", group="readiness",
                           outcome="incomplete", **selected),
                    *[alert_series(name, "pending", "_ 1 stale _ _ _ _", **labels) for name, labels in alerts],
                    # A lingering firing sample must yield to a failed scrape.
                    *[alert_series(name, "firing", "_ _ 1 stale 1 stale _", **labels) for name, labels in alerts],
                    alert_series("UnrelatedWarning", "firing", "1x6", **other),
                ], [
                    *[check(WARNING_TIMELINE_PANEL, sample(state, **selected),
                            eval_time=f"{minute}m", instance=selected["instance"], ref_id=ref_id)
                      for minute, (state, _, _) in enumerate(warning_transitions) for ref_id in ("A", "B")],
                    *[check(WARNING_TIMELINE_PANEL, sample(state, group="readiness", **selected),
                            *[sample(other_state, group=group, **selected)
                              for group in ("identity", "process", "loop_health")],
                            eval_time=f"{minute}m", instance=selected["instance"], ref_id="C")
                      for minute, (_, state, other_state) in enumerate(warning_transitions)],
                    check(WARNING_TIMELINE_PANEL, sample(2, **selected), sample(-1, **other), eval_time="2m"),
                    check(WARNING_TIMELINE_PANEL, sample(-1, **other), eval_time="2m", instance=other["instance"]),
                    check(WARNING_TIMELINE_PANEL, sample(2, group="readiness", **selected), eval_time="2m",
                          instance=selected["instance"], ref_id="C", group="readiness"),
                    check(WARNING_TIMELINE_PANEL, sample(-2, group="readiness", **selected), eval_time="4m",
                          instance=selected["instance"], ref_id="C", group="readiness"),
                    check(WARNING_TIMELINE_PANEL, sample(-1, group="identity", **selected), eval_time="6m",
                          instance=selected["instance"], ref_id="C", group="identity"),
                    check(WARNING_TIMELINE_PANEL, eval_time="2m", instance=selected["instance"],
                          ref_id="C", group="builder_health"),
                    check(WARNING_TIMELINE_PANEL, sample(2, **selected), eval_time="2m",
                          instance=selected["instance"], ref_id="B", group="readiness"),
                ]),
                case("one evidence group scopes failure, duration, and evaluation panels", [
                    *[series(metric, "0 0 0 0 1 1", group=group, **selected, **labels)
                      for group in ("identity", "readiness")
                      for metric, labels in (
                          ("incomplete_reports_total", {}),
                          ("group_evaluations_total", {"outcome": "complete"}),
                          ("group_evaluations_total", {"outcome": "incomplete"}),
                          ("core_evidence_gaps_total", {"reason": "missing"}),
                          ("query_attempts_total", {"outcome": "source_error"}),
                          ("query_duration_seconds_bucket", {"le": "1"}),
                          ("query_duration_seconds_bucket", {"le": "+Inf"}),
                          ("query_source_errors_total", {"reason": "http_error"}),
                      )],
                    *[series("preview_omitted_items_total", "0 0 0 0 1 1", group=group, **selected)
                      for group in ("identity", "builder_health")],
                ], [
                    check(INCOMPLETE_GROUPS_PANEL, sample(1, group="identity"), sample(1, group="readiness")),
                    *[check(panel_id, sample(value, group="readiness", **labels),
                            instance=selected["instance"], ref_id=ref_id, group="readiness")
                      for panel_id, ref_id, value, labels in (
                          (INCOMPLETE_GROUPS_PANEL, "A", 1, {}),
                          (INCOMPLETE_RATE_PANEL, "A", 0.5, {}),
                          (INCOMPLETE_RATE_PANEL, "B", 2, {}),
                          (CORE_GAPS_PANEL, "A", 1, {"reason": "missing"}),
                          (5, "A", 1, {"outcome": "source_error"}),
                          (6, "A", 0.95, {}),
                          (SOURCE_FAILURES_PANEL, "A", 1, {"instance": selected["instance"], "reason": "http_error"}),
                      )],
                    check(8, sample(1, group="identity"), instance=selected["instance"], group="identity"),
                ]),
                case("core warning timeline requires ten recent evaluations per replica and group", [
                    scrape("1x76", **selected),
                    scrape("1x76", **other),
                    # Six complete and four incomplete evaluations before a quiet period.
                    # Both counters reset at minute 36. Evaluations resume at minute 57.
                    series("group_evaluations_total", "0+1x6 6x28 0x19 0+1x20",
                           group="readiness", outcome="complete", **selected),
                    series("group_evaluations_total", "0x6 1+1x3 4x24 0x33 stale",
                           group="readiness", outcome="incomplete", **selected),
                    series("group_evaluations_total", "0x76", group="identity", outcome="incomplete", **selected),
                    series("group_evaluations_total", "0+2x76", group="readiness", outcome="complete", **other),
                    series("group_evaluations_total", "0x76", group="readiness", outcome="incomplete", **other),
                ], [
                    *[check(WARNING_TIMELINE_PANEL, sample(state, group="readiness", **selected),
                            sample(-3, group="identity", **selected),
                            sample(-1, group="process", **selected),
                            sample(-1, group="loop_health", **selected),
                            eval_time=f"{minute}m", instance=selected["instance"], ref_id="C")
                      for minute, state in ((0, -3), (9, -3), (10, 0), (25, 0), (35, -3),
                                            (36, -3), (50, -3), (68, 0), (70, -1))],
                    check(WARNING_TIMELINE_PANEL, sample(0, group="readiness", **other),
                          *[sample(-1, group=group, **other) for group in ("identity", "process", "loop_health")],
                          eval_time="35m", instance=other["instance"], ref_id="C"),
                ]),
                case("revision and restart markers follow observed changes by instance", [
                    scrape("1x7", **selected),
                    scrape("1x7", **other),
                    build_info("a" * 40, "1 1 stale _ _ _ _ _", **selected),
                    build_info("b" * 40, "_ _ 1 1 stale _ _ _", **selected),
                    build_info("a" * 40, "_ _ _ _ 1 1 stale _", build_time="rollback", **selected),
                    build_info("a" * 40, "_ _ _ _ _ _ 1 1", build_time="same-revision-restart", **selected),
                    build_info("a" * 40, "1 1 1 stale _ _ _ _", **other),
                    build_info("c" * 40, "_ _ _ 1 1 1 1 1", **other),
                    uptime("120 135 1 16 1 16 1 16", **selected),
                    uptime("60 75 90 1 16 31 46 61", **other),
                ], [
                    revision_at("0s"),
                    revision_at("15s"),
                    revision_at("30s", sample(30000, revision="bbbbbbb", **selected)),
                    revision_at("40s", sample(30000, revision="bbbbbbb", **selected)),
                    revision_at("45s", sample(45000, revision="ccccccc", **other)),
                    revision_at("45s", instance=selected["instance"]),
                    revision_at("60s", sample(60000, revision="aaaaaaa", **selected), instance=selected["instance"]),
                    revision_at("60s", instance=other["instance"]),
                    revision_at("75s"),
                    revision_at("90s"),
                    revision_at("105s"),
                    restart_at("0s"),
                    restart_at("15s"),
                    restart_at("30s", sample(30000, **selected)),
                    restart_at("40s", sample(30000, **selected)),
                    restart_at("45s", sample(45000, **other)),
                    restart_at("45s", instance=selected["instance"]),
                    restart_at("60s", sample(60000, **selected)),
                    restart_at("75s"),
                    restart_at("90s", sample(90000, **selected), instance=selected["instance"]),
                    restart_at("90s", instance=other["instance"]),
                    restart_at("105s"),
                ], interval="15s"),
                case("restart markers require uptime on adjacent successful scrapes", [
                    scrape("1 1 0 1 1 _ 1 1 1 1 1 1", **selected),
                    uptime("100 115 stale 2 17 _ 1 16 stale 2 17 8", **selected),
                ], [
                    *[restart_at(f"{second}s") for second in range(15, 165, 15)],
                    restart_at("165s", sample(165000, **selected)),
                ], interval="15s"),
                case("restart markers reject uptime retained from an earlier scrape", [
                    scrape("_ _ _ _ 1 _ 1 _ _ 1", **selected),
                    uptime("_ _ _ _ 100 _ _ _ _ 10", **selected),
                ], [restart_at("45s")], interval="5s"),
                case("revision markers require reliable adjacent build observations", [
                    scrape("1 0 1 _ 1 1 1 1 1 1", **selected),
                    build_info("a" * 40, "1 stale _ _ _ _ _ _ _ 1", **selected),
                    build_info("b" * 40, "_ _ 1 _ stale _ _ _ _ _", **selected),
                    build_info("c" * 40, "_ _ _ _ 1 1 1 stale _ _", **selected),
                    build_info("d" * 40, "_ _ _ _ _ _ 1 1 stale _", **selected),
                    build_info("unknown", "_ _ _ _ _ _ _ _ 1 stale", **selected),
                ], [
                    revision_at("30s"),
                    revision_at("60s"),
                    revision_at("75s"),
                    revision_at("90s"),
                    revision_at("105s"),
                    revision_at("120s"),
                    revision_at("135s"),
                ], interval="15s"),
                case("instance selection scopes counter aggregation and scrape coverage", [
                    scrape("1x5", **selected),
                    scrape("1x4 0", **other),
                    series("active_collections", "0x5", **selected),
                    series("reports_total", "0 0 0 1 1 1", outcome="incomplete", **selected),
                    series("reports_total", "0 0 1 1 2 2", outcome="complete", **other),
                    series("incomplete_reports_total", "0 0 0 1 1 1", group="readiness", **selected),
                ], [
                    check(3, sample(2, outcome="complete"), sample(1, outcome="incomplete")),
                    check(3, sample(1, outcome="incomplete"), instance=selected["instance"]),
                    check(3, sample(2, outcome="complete"), instance=other["instance"]),
                    check(INCOMPLETE_GROUPS_PANEL, sample(1, group="readiness"), instance=selected["instance"]),
                    check(INCOMPLETE_GROUPS_PANEL, instance=other["instance"]),
                    check(COVERAGE_PANEL, sample(2, **selected), instance=selected["instance"]),
                    check(COVERAGE_PANEL, sample(0, **other), instance=other["instance"]),
                    check(COVERAGE_PANEL, no_targets, instance="removed-apid:9101"),
                ]),
                case("source failure reasons stay scoped after a counter reset", [
                    series("query_source_errors_total", "0 1 1 0 1 1",
                           group="identity", reason="http_error", **selected),
                    series("query_source_errors_total", "0 0 0 0 1 1",
                           group="readiness", reason="transport_error", **selected),
                    series("query_source_errors_total", "0 0 0 0 4 4",
                           group="identity", reason="invalid_response", **other),
                    series("query_source_errors_total", "3x5", group="process", reason="other", **other),
                    series("query_source_errors_total", "0+1x5", job="other",
                           group="loop_health", reason="http_error"),
                    series("query_attempts_total", "0 0 0 0 1 1",
                           group="identity", outcome="source_error", **c),
                ], [
                    check(SOURCE_FAILURES_PANEL, sample(2, instance=selected["instance"], group="identity", reason="http_error"),
                          sample(1, instance=selected["instance"], group="readiness", reason="transport_error"),
                          sample(4, instance=other["instance"], group="identity", reason="invalid_response")),
                    check(SOURCE_FAILURES_PANEL, sample(2, instance=selected["instance"], group="identity", reason="http_error"),
                          sample(1, instance=selected["instance"], group="readiness", reason="transport_error"),
                          instance=selected["instance"]),
                    check(SOURCE_FAILURES_PANEL, instance=c["instance"]),
                    check(5, sample(1, group="identity", outcome="source_error"), instance=c["instance"]),
                ]),
                case("incomplete rate and evaluation count share scope and survive a reset", [
                    series("group_evaluations_total", "0x4 1 0 1x23", group="readiness", outcome="incomplete", **selected),
                    series("group_evaluations_total", "0x4 2 0 4x23", group="readiness", outcome="complete", **selected),
                    series("group_evaluations_total", "0x30", group="readiness", outcome="incomplete", **other),
                    series("group_evaluations_total", "0x4 8x25", group="readiness", outcome="complete", **other),
                    series("group_evaluations_total", "0x30", group="builder_health", outcome="incomplete", **selected),
                    series("group_evaluations_total", "0x30", group="builder_health", outcome="complete", **selected),
                    series("group_evaluations_total", "0+1x30", group="readiness", outcome="incomplete", job="other"),
                    series("core_evidence_gaps_total", "0x4 1 0 1x23", group="readiness", reason="missing", **selected),
                    series("core_evidence_gaps_total", "0x4 1x25", group="readiness", reason="conflicting", **selected),
                    series("core_evidence_gaps_total", "0x4 1x25", group="identity", reason="stale", **other),
                    series("core_evidence_gaps_total", "0x30", group="identity", reason="missing", **other),
                    series("core_evidence_gaps_total", "0+1x30", group="readiness", reason="stale", job="other"),
                    # Older apid versions only publish the five-minute panel's counter.
                    series("incomplete_reports_total", "0+1x30", group="readiness", instance="legacy:9101"),
                ], [
                    check(INCOMPLETE_RATE_PANEL, sample(0.125, group="readiness"), eval_time="30m"),
                    check(INCOMPLETE_RATE_PANEL, sample(16, group="readiness"), eval_time="30m", ref_id="B"),
                    check(INCOMPLETE_RATE_PANEL, sample(0.25, group="readiness"), eval_time="30m", instance=selected["instance"]),
                    check(INCOMPLETE_RATE_PANEL, sample(8, group="readiness"), eval_time="30m", instance=selected["instance"], ref_id="B"),
                    check(INCOMPLETE_RATE_PANEL, sample(0, group="readiness"), eval_time="30m", instance=other["instance"]),
                    check(INCOMPLETE_RATE_PANEL, sample(8, group="readiness"), eval_time="30m", instance=other["instance"], ref_id="B"),
                    check(INCOMPLETE_RATE_PANEL, eval_time="30m", instance="legacy:9101"),
                    check(INCOMPLETE_RATE_PANEL, eval_time="30m", instance="legacy:9101", ref_id="B"),
                    check(CORE_GAPS_PANEL, sample(2, group="readiness", reason="missing"),
                          sample(1, group="readiness", reason="conflicting"), sample(1, group="identity", reason="stale"), eval_time="30m"),
                    check(CORE_GAPS_PANEL, sample(2, group="readiness", reason="missing"),
                          sample(1, group="readiness", reason="conflicting"), eval_time="30m", instance=selected["instance"]),
                    check(CORE_GAPS_PANEL, sample(1, group="identity", reason="stale"), eval_time="30m", instance=other["instance"]),
                    check(CORE_GAPS_PANEL, eval_time="30m", instance="legacy:9101"),
                ]),
                case("rolling upgrade and failed scrape with a retained gauge", during_upgrade, [
                    check(COVERAGE_PANEL, sample(2, **a), sample(1, **b), sample(2, **c), eval_time="4m"),
                    check(COVERAGE_PANEL, sample(2, **a), sample(2, **b), sample(0, **c)),
                ]),
                case("last target removed while gauge stays in lookback", [
                    scrape("1x4 stale"),
                    series("active_collections", "0x4 _"),
                ], [check(COVERAGE_PANEL, no_targets)]),
                case("report age advances until a complete report or restart", [
                    scrape("1x5", **a),
                    series("last_complete_report_timestamp_seconds", "0 55 55 55 0 295", **a),
                    # This replica begins emitting the gauge after an upgrade.
                    scrape("1x5", **b),
                    series("last_complete_report_timestamp_seconds", "_ _ _ _ _ 302", **b),
                    # An older gauge sample must not survive a failed scrape in the panel.
                    scrape("_ _ _ _ 1 0", **c),
                    series("last_complete_report_timestamp_seconds", "_ _ _ _ 235 _", **c),
                ], [
                    check(REPORT_AGE_PANEL, sample(-1, **a), eval_time="0m"),
                    check(REPORT_AGE_PANEL, sample(5, **a), eval_time="1m"),
                    check(REPORT_AGE_PANEL, sample(125, **a), eval_time="3m"),
                    check(REPORT_AGE_PANEL, sample(-1, **a), sample(5, **c), eval_time="4m"),
                    check(REPORT_AGE_PANEL, sample(5, **a), sample(0, **b)),
                ]),
                case("latest collection outcome and age follow each apid independently", [
                    scrape("1 1 1 1 1 0 1 1 stale", **selected),
                    series("last_collection_timestamp_seconds", "0 stale _ _ _ _ 0 stale",
                           outcome="not_collected", **selected),
                    series("last_collection_timestamp_seconds", "_ 55 stale _ 235 _ stale",
                           outcome="complete", **selected),
                    series("last_collection_timestamp_seconds", "_ _ 118 118 stale",
                           outcome="deadline_exceeded", **selected),
                    series("last_complete_report_timestamp_seconds", "0 55 55 55 235 _ 0 stale", **selected),
                    scrape("1x8", **other),
                    series("last_collection_timestamp_seconds", "_ _ 122x6",
                           outcome="incomplete", **other),
                ], [
                    *[check(LAST_COLLECTION_PANEL, sample(age, outcome=outcome, **selected),
                            eval_time=f"{minute}m", instance=selected["instance"])
                      for minute, age, outcome in (
                          (0, -1, "not_collected"), (1, 5, "complete"),
                          (2, 2, "deadline_exceeded"), (3, 62, "deadline_exceeded"),
                          (4, 5, "complete"), (5, -1, "scrape_failed"),
                          (6, -1, "not_collected"), (7, -1, "metrics_missing"),
                      )],
                    check(LAST_COLLECTION_PANEL, sample(-1, outcome="metrics_missing", **other),
                          eval_time="1m", instance=other["instance"]),
                    check(LAST_COLLECTION_PANEL, sample(2, outcome="deadline_exceeded", **selected),
                          sample(0, outcome="incomplete", **other), eval_time="2m"),
                    check(LAST_COLLECTION_PANEL, eval_time="8m", instance=selected["instance"]),
                    check(REPORT_AGE_PANEL, sample(125, **selected), eval_time="3m", instance=selected["instance"]),
                    check(REPORT_AGE_PANEL, sample(5, **selected), eval_time="4m", instance=selected["instance"]),
                ]),
                case("idle telemetry is zero for traffic and absent for percentiles", idle, [
                    check(COVERAGE_PANEL, sample(2, **a)),
                    check(1, sample(0, **active, **a)),
                    check(2, sample(0, **a)),
                    check(3, sample(0, outcome="complete")),
                    check(INCOMPLETE_GROUPS_PANEL),
                    *[check(i) for i in (4, 5, 6, 7, 8, 9, 10, 11)],
                ]),
                case("one replica saturates and counters reset independently", saturated_and_restarted, [
                    check(COVERAGE_PANEL, sample(2, **a), sample(2, **b)),
                    check(1, sample(2, **active, **a), sample(0, **active, **b)),
                    check(2, sample(3, **a), sample(0, **b)),
                    check(3, sample(7, outcome="incomplete"), sample(1, outcome="deadline_exceeded")),
                    check(INCOMPLETE_GROUPS_PANEL, sample(7, group="readiness"), sample(1, group="loop_health")),
                    check(4, sample(0.95, instance=a["instance"])),
                    check(5, sample(3, group="identity", outcome="deadline_exceeded"),
                          sample(4, group="builder_health", outcome="source_error")),
                    check(6, sample(0.2375, group="identity")),
                    check(7, sample(63897.6, instance=a["instance"])),
                    check(8, sample(15, group="identity")),
                    check(9, sample(0.95, instance=a["instance"], phase="inventory"),
                          sample(0.2375, instance=b["instance"], phase="core_evidence")),
                    check(10, sample(3, instance=a["instance"], outcome="deadline_exceeded"),
                          sample(4, instance=b["instance"], outcome="source_error")),
                    check(11, sample(3, instance=a["instance"], phase="inventory", origin="caller_deadline"),
                          sample(4, instance=a["instance"], phase="core_evidence", origin="collection_budget"),
                          sample(1, instance=b["instance"], phase="inventory", origin="collection_budget")),
                ]),
            ],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "runtime-diagnostics.test.yml"
            path.write_text(json.dumps(fixtures))
            result = subprocess.run([PROMTOOL, "test", "rules", str(path)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
