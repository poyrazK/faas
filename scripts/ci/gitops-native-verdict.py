#!/usr/bin/env python3
"""Verify private v2 evidence and export an allowlisted CI summary, never logs."""
import argparse
import json
import os
from pathlib import Path
import re
import stat
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import gitops_native_report as report

STAGES = ("preflight", "leak_before", "journey", "leak_after", "source")
SETUP_REASONS = ("setup_failed", "root_required", "unsupported_host", "undesignated_host",
                 "host_configuration_missing", "host_configuration_permissions", "source_mismatch",
                 "pinned_go_missing", "pinned_go_mismatch", "native_runner_failed", "interrupted", "completed")


def read_json(path):
    fd = os.open(path, os.O_RDONLY | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > 256 * 1024:
            raise ValueError("invalid evidence file")
        data = stream.read(256 * 1024 + 1)
        if len(data) > 256 * 1024:
            raise ValueError("oversized evidence file")
        value = json.loads(data)
        if not isinstance(value, dict):
            raise ValueError("invalid evidence object")
        return value


def verify(directory, expected_sha, runner_outcome="success"):
    """Only actual complete native evidence at the dispatch SHA can pass."""
    summary = {"schema": "gregale.dev/gitops-native-ci/v1", "report_schema": report.SCHEMA,
               "expected_commit": expected_sha if re.fullmatch(r"[a-f0-9]{40}", expected_sha) else None,
               "source_commit": None,
               "result": "blocked", "native_acceptance_passed": False,
               "source_scheduled_jobs_passed": False, "production_enforcement_enabled": False,
               "coverage": report.required_coverage(), "stages": {}, "tests": {}, "errors": []}
    errors = summary["errors"]
    if summary["expected_commit"] is None:
        errors.append("invalid_expected_sha")
    if runner_outcome != "success":
        errors.append("ci_runner_not_successful")
    try:
        setup = read_json(directory / "setup.json")
        if setup.get("phase") not in ("setup", "native") or setup.get("reason") not in SETUP_REASONS or type(setup.get("exit_code")) is not int:
            raise ValueError("invalid setup evidence")
        if setup["phase"] == "native":
            summary["result"] = "failed"
        if setup != {"phase": "native", "reason": "completed", "exit_code": 0}:
            errors.append("runner_" + setup["reason"])
    except (OSError, ValueError):
        errors.append("setup_evidence_missing_or_invalid")
    private = directory / "private"
    try:
        value = read_json(private / "report.json")
        if value.get("schema") != report.SCHEMA or value.get("coverage") != report.required_coverage() or value.get("evidence_kind") != "local_operator_run":
            raise ValueError("invalid report contract")
        if value.get("host_class") != "native" or value.get("production_enforcement_enabled") is not False:
            errors.append("unsupported_scope_or_host")
        if value["initial"]["source"]["commit"] != expected_sha or value["final"]["source"]["commit"] != expected_sha:
            errors.append("source_sha_mismatch")
        stages = value["stages"]
        if set(stages) != set(STAGES) or any(stages[key] not in ("not_run", "pass", "fail", "blocked") for key in STAGES):
            raise ValueError("invalid stage evidence")
        summary["stages"] = {key: stages[key] for key in STAGES}
        if type(value["command_exit_code"]) is not int:
            raise ValueError("invalid runner exit")
        if report.fingerprint(private / "test-events.jsonl") is None:
            raise ValueError("missing or nonregular event stream")
        events = report.read_events(private / "test-events.jsonl")
        summary["tests"] = {key: events["tests"][key] for key in report.required_tests()}
        observed = report.assess(value, value["final"], stages, "", value["command_exit_code"], events)
        if observed["result"] != "passed" or not observed["native_acceptance_passed"] or not observed["source_scheduled_jobs_passed"]:
            errors.append("native_matrix_or_metadata_incomplete")
        if any(value.get(key) != observed[key] for key in observed):
            errors.append("reported_verdict_mismatch")
        fingerprints = {name: report.fingerprint(private / name) for name in report.ARTIFACTS}
        if any(item is None for item in fingerprints.values()) or value.get("artifacts") != fingerprints:
            errors.append("artifact_fingerprints_mismatch")
        # Hashes bind the summary to the private operator record without
        # copying its host identity, tool output, credentials or raw diagnostics.
        summary["operator_report"] = report.fingerprint(private / "report.json")
        summary["artifacts"] = fingerprints
        if not errors:
            summary.update(result="passed", source_commit=expected_sha, native_acceptance_passed=True, source_scheduled_jobs_passed=True)
    except (OSError, ValueError, KeyError, TypeError, AttributeError):
        errors.append("native_report_missing_or_invalid")
    return summary


def publish(directory, summary):
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    public = directory / "public"
    public.mkdir(mode=0o755, exist_ok=True)
    public.chmod(0o755)
    report.write_json(public / "summary.json", summary)
    lines = ["## GitOps native acceptance", "", f"Result: **{summary['result']}**",
             f"Dispatch commit: `{summary['expected_commit'] or 'unverified'}`", "",
             f"Required test nodes passed: {sum(state == 'pass' for state in summary['tests'].values())}/{len(report.required_tests())}",
             "", "Production qualification and enforcement remain disabled.",
             "Provider access uses local fixtures; live provider and protected review qualification are outside this evidence.", ""]
    if summary["errors"]:
        lines.extend(["Evidence failures:", "", *[f"- `{code}`" for code in summary["errors"]], ""])
    lines.extend(["Raw logs remain private on the exclusive host. This summary grants no production authorization.", ""])
    (public / "summary.md").write_text("\n".join(lines))
    for name in ("summary.json", "summary.md"):
        (public / name).chmod(0o644)
    # The Actions account may traverse to the two sanitized files, while the
    # native runner's private directory and setup files retain 0700/0600 modes.
    directory.chmod(0o711)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", required=True, type=Path)
    parser.add_argument("--expected-sha", required=True)
    parser.add_argument("--runner-outcome", required=True, choices=("success", "failure", "cancelled", "skipped"))
    args = parser.parse_args()
    if not args.directory.is_absolute() or args.directory.resolve().is_relative_to(Path(__file__).resolve().parents[2]):
        parser.error("evidence directory must be absolute and outside the checkout")
    summary = verify(args.directory, args.expected_sha, args.runner_outcome)
    publish(args.directory, summary)
    print("GitOps native CI: " + summary["result"])
    return 0 if summary["native_acceptance_passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
