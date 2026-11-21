#!/usr/bin/env python3
"""Sanitized first-hour scorecard from existing journey and agent-eval evidence.

Missing, skipped and fixture evidence never become successful live runs. Timing
starts after authentication; interactive installation/login remains a separate
manual acceptance lane. Raw logs, response bodies and agent answers are omitted.
"""
from __future__ import annotations

import argparse
import json
import math
import statistics
from pathlib import Path

STEPS = (
    "first_private_response", "anonymous_request_denied", "edit_and_interrupt_wait",
    "resume_same_release_and_verify_edit", "failed_startup_is_diagnosable",
    "repair_and_verify", "cleanup",
)
METRICS = (
    "first_hour_first_verified_response_s", "first_hour_second_verified_deployment_s",
    "first_hour_resume_to_verified_response_s", "first_hour_failure_to_recovery_s",
    "first_hour_complete_s", "first_hour_interruptions",
)
AGENT_TASKS = ("first-hour-private", "first-hour-edit", "fix-failed-deploy")
AGENTS = ("claude", "codex", "gemini", "cursor")
TOOLSETS = ("core", "release", "diagnose", "all", "none")


def finite(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value) and value >= 0


def read_jsonl(path, tolerant=False):
    if path is None:
        return []
    rows = []
    for number, line in enumerate(Path(path).read_text().splitlines(), 1):
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except ValueError as error:
            if tolerant:
                rows.append({"Action": "fail", "parse_error": True})
                continue
            raise ValueError(f"invalid JSON on evidence line {number}") from error
        if not isinstance(row, dict):
            raise ValueError(f"evidence line {number} must be an object")
        rows.append(row)
    return rows


def scorecard(events, agents, metadata):
    steps = {name: {"status": "not_run", "seconds": None} for name in STEPS}
    metrics = {name: None for name in METRICS}
    package_failed, parent_failed = False, False
    for event in events:
        test, action = event.get("Test", ""), event.get("Action")
        if action == "fail" and not test:
            package_failed = True
        if test == "TestFirstHourCLI" and action == "fail":
            parent_failed = True
        prefix = "TestFirstHourCLI/"
        if not test.startswith(prefix):
            continue
        name = test[len(prefix):]
        if name not in steps:
            continue
        if action in ("pass", "fail", "skip"):
            elapsed = event.get("Elapsed")
            if elapsed is not None and not finite(elapsed):
                raise ValueError("journey duration must be finite and nonnegative")
            steps[name] = {"status": {"pass": "passed", "fail": "failed", "skip": "blocked"}[action], "seconds": elapsed}
        if action == "output":
            # The journey's existing metric name=value protocol, restricted to
            # this pack's names. Never copy arbitrary logs into the report.
            for metric in METRICS:
                marker = "metric " + metric + "="
                if marker in event.get("Output", ""):
                    value = float(event["Output"].split(marker, 1)[1].split()[0])
                    if not finite(value):
                        raise ValueError("journey metric must be finite and nonnegative")
                    metrics[metric] = value
    if package_failed or parent_failed or any(s["status"] == "failed" for s in steps.values()):
        cli_status = "failed"
    elif all(s["status"] == "passed" for s in steps.values()) and all(v is not None for v in metrics.values()):
        cli_status = "passed"
    else:
        cli_status = "not_measured" if not events else "incomplete"
    if not events and metadata.get("release_verify_available") is False:
        cli_status = "blocked"
    if cli_status == "passed" and (metrics["first_hour_interruptions"] != 1 or metrics["first_hour_complete_s"] > 3600
                                  or not metrics["first_hour_first_verified_response_s"] <= metrics["first_hour_second_verified_deployment_s"] <= metrics["first_hour_complete_s"]):
        cli_status = "failed"
    if metadata.get("evidence_kind") != "deployed" and cli_status == "passed":
        cli_status = "fixture_only"

    agent_rows = []
    excluded_agents = 0
    for task in AGENT_TASKS:
        candidates = [r for r in agents if r.get("task") == task]
        runs = [r for r in candidates if metadata.get("cli_sha256") and r.get("cli_sha256") == metadata["cli_sha256"]
                and metadata.get("target") and str(r.get("target", "")).rstrip("/") == metadata["target"].rstrip("/")]
        excluded_agents += len(candidates) - len(runs)
        if any(r.get("agent") not in AGENTS or r.get("toolset") not in TOOLSETS for r in runs):
            raise ValueError("agent evidence uses an unsupported agent or toolset")
        groups = sorted({(str(r.get("agent", "unknown")), str(r.get("toolset", "unknown"))) for r in runs})
        if not groups:
            agent_rows.append({"task": task, "agent": None, "toolset": None, "runs": 0, "passed": 0,
                               "median_seconds": None, "median_tool_calls": None, "follow_ups": None})
        for agent, toolset in groups:
            selected = [r for r in runs if str(r.get("agent", "unknown")) == agent and str(r.get("toolset", "unknown")) == toolset]
            durations = [r["duration_s"] for r in selected if finite(r.get("duration_s"))]
            calls = [sum(r["tool_calls"].values()) for r in selected if isinstance(r.get("tool_calls"), dict)
                     and all(finite(n) for n in r["tool_calls"].values())]
            agent_rows.append({"task": task, "agent": agent, "toolset": toolset, "runs": len(selected),
                               "passed": sum(r.get("success") is True and not r.get("left_behind") for r in selected),
                               "median_seconds": statistics.median(durations) if durations else None,
                               "median_tool_calls": statistics.median(calls) if calls else None,
                               "follow_ups": sum(r.get("follow_ups", 0) for r in selected) if all(finite(r.get("follow_ups", 0)) for r in selected) else None})
    # Only allow provenance fields. In particular, do not copy tokens, account
    # email, response previews, agent transcripts or local filesystem paths.
    provenance = {key: metadata.get(key) for key in (
        "target", "cli_version", "release_verify_available", "harness_revision", "harness_worktree_dirty", "cli_sha256", "plan", "evidence_kind", "started_at",
    )}
    measured_agents = sum(row["runs"] for row in agent_rows)
    if any(row["runs"] > row["passed"] for row in agent_rows):
        agent_status = "failed"
    elif all(any(row["task"] == task and row["runs"] for row in agent_rows) for task in AGENT_TASKS):
        agent_status = "passed"
    elif measured_agents:
        agent_status = "partial"
    else:
        agent_status = "incompatible" if excluded_agents else "not_measured"
    if measured_agents and metadata.get("evidence_kind") != "deployed":
        agent_status = "fixture_only"
    return {"schema_version": 1, "provenance": provenance,
            "coverage": {"install_login": "not_measured", "interactive_start": "not_measured",
                         "scripted_cli": cli_status, "agent_tasks": agent_status},
            "steps": steps, "metrics": metrics, "agents": agent_rows,
            "excluded_agent_records": excluded_agents,
            "limitations": ["Scripted timings start after authentication and exclude human thinking time.",
                            "This scripted CLI report does not qualify install/login or interactive Start; use the separate onboarding report.",
                            "One run is a baseline observation, not a percentile or reliability estimate.",
                            "Disposable-app releases use immediate rollouts; default safe-rollout latency is not measured.",
                            "Agent follow-up counts measure harness assistance, not all possible human intervention.",
                            "Account plan is recorded; a Scale run does not qualify Free-plan behavior."]}


def markdown(report):
    p, coverage = report["provenance"], report["coverage"]
    out = ["# First-hour baseline", "", f"Scripted CLI outcome: **{coverage['scripted_cli']}**.", "",
           "| Provenance | Value |", "|---|---|"]
    for key, value in p.items():
        displayed = "not recorded" if value is None or value == "" else str(value)
        out.append(f"| {key} | {displayed.replace('|', '/').replace(chr(10), ' ')} |")
    out += ["", "## Acceptance coverage", "", "| Lane | Evidence |", "|---|---|"]
    out.extend(f"| {key} | {value} |" for key, value in coverage.items())
    out += ["", "## Scripted journey", "", "| Step | Result | Seconds |", "|---|---|---|"]
    for name, step in report["steps"].items():
        out.append(f"| {name} | {step['status']} | {step['seconds'] if step['seconds'] is not None else '—'} |")
    out += ["", "## Timings", "", "| Measurement | Value |", "|---|---|"]
    out.extend(f"| {key} | {value if value is not None else 'not measured'} |" for key, value in report["metrics"].items())
    out += ["", "## Coding agents", "", "| Task | Agent / toolset | Passed / runs | Median seconds | Median tool calls | Follow-ups |",
            "|---|---|---|---|---|---|"]
    for row in report["agents"]:
        fmt = lambda v: "—" if v is None else str(v)
        out.append(f"| {row['task']} | {fmt(row['agent'])} / {fmt(row['toolset'])} | {row['passed']} / {row['runs']} | {fmt(row['median_seconds'])} | {fmt(row['median_tool_calls'])} | {fmt(row['follow_ups'])} |")
    out += ["", f"Agent records excluded for missing or mismatched target/CLI checksum: {report['excluded_agent_records']}."]
    out += ["", "## Interpretation", ""]
    out.extend("- " + limitation for limitation in report["limitations"])
    return "\n".join(out) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--journey", type=Path)
    parser.add_argument("--agents", type=Path)
    parser.add_argument("--metadata", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    report = scorecard(read_jsonl(args.journey, tolerant=True), read_jsonl(args.agents), json.loads(args.metadata.read_text()))
    args.out.mkdir(parents=True, exist_ok=True)
    (args.out / "baseline.json").write_text(json.dumps(report, indent=2, allow_nan=False) + "\n")
    (args.out / "baseline.md").write_text(markdown(report))
    print(args.out / "baseline.md")
    return int(report["coverage"]["scripted_cli"] != "passed")


if __name__ == "__main__":
    raise SystemExit(main())
