#!/usr/bin/env python3
"""Run existing strict journeys serially; publish only their bounded reports.

Private test output stays on the designated host. Aggregate evidence is a guest
acceptance result, not provider qualification, operator approval or attestation.
"""
import argparse
import datetime as dt
import hashlib
import importlib.util
import json
import os
import re
import subprocess
from pathlib import Path

from native_guest_evidence import timestamp, write_report

REPO = Path(__file__).resolve().parents[2]
SUITES = json.loads((REPO / "scripts/ci/binding-native-suites.json").read_text())
SHARED = ("guest_init", "kernel", "builder_base", "firecracker", "jailer")


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()).hexdigest()


def load_verdict(suite):
    spec = importlib.util.spec_from_file_location("binding_" + suite["name"], REPO / suite["verdict"])
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def verify_reports(directory, revision, now=None, publish=False):
    """Rebuild every report from its private log using the existing validators.

    Runner exit success alone cannot supply a missing/partial receipt. Replaying
    the validators also rejects skipped/failed phases and changed report fields.
    """
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("source_revision_invalid")
    now = now or dt.datetime.now(dt.UTC)
    results, common, previous, verified = [], None, None, []
    if [suite["name"] for suite in SUITES] != ["postgres", "storage", "queue", "service"]:
        raise ValueError("suite_inventory_incomplete")
    for suite in SUITES:
        report_path = directory / (suite["name"] + ".report.json")
        private_path = directory / (suite["name"] + ".jsonl")
        if report_path.is_symlink() or private_path.is_symlink():
            raise ValueError("evidence_symlink")
        raw = private_path.read_bytes()
        if report_path.stat().st_size > 65536:
            raise ValueError("report_too_large")
        report = json.loads(report_path.read_text())
        validator = load_verdict(suite)
        rebuilt = validator.make_report(raw, "native", True, True, True, 86400)
        if report != rebuilt:
            raise ValueError("report_does_not_match_execution")
        started, completed, expiry = (timestamp(report[field]) for field in ("started_at", "completed_at", "expires_at"))
        if not started < completed <= now < expiry or (previous is not None and started < previous):
            raise ValueError("evidence_stale_future_or_overlapping")
        previous = completed
        manifests = ([journey["identity"] for journey in report["journeys"]]
                     if suite["name"] == "storage" else [report["runtime"]])
        for manifest in manifests:
            if manifest["source_revision"] != revision:
                raise ValueError("source_revision_mismatch")
            shared = {key: manifest["artifacts"][key] for key in SHARED}
            if common is not None and shared != common:
                raise ValueError("shared_runtime_bundle_changed")
            common = shared
        verified.append((suite["name"], rebuilt))
        results.append(dict(name=suite["name"], root_test=report["root_test"], passed=True,
                            report=suite["name"] + ".json", report_sha256=hashlib.sha256(
                                (json.dumps(rebuilt, sort_keys=True, indent=2, allow_nan=False) + "\n").encode()).hexdigest(),
                            started_at=report["started_at"], completed_at=report["completed_at"], expires_at=report["expires_at"]))
    if publish:
        for name, report in verified:
            write_report(directory / "public" / (name + ".json"), report)
    summary = dict(version=2, source_revision=revision, native_guest_qualified=True,
                   release_qualified=False, suites=results, shared_artifacts=common,
                   expires_at=min(result["expires_at"] for result in results))
    summary["evidence_sha256"] = digest(summary)
    return summary


def run(source, out, go):
    if not re.fullmatch(r"[0-9a-f]{40}", source):
        raise ValueError("source_revision_invalid")
    if subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip() != source:
        raise ValueError("source_revision_mismatch")
    if subprocess.check_output(["git", "status", "--porcelain"], cwd=REPO):
        raise ValueError("source_dirty")
    if [suite["name"] for suite in SUITES] != ["postgres", "storage", "queue", "service"]:
        raise ValueError("suite_inventory_incomplete")
    out.mkdir(mode=0o700)  # Never reuse earlier evidence or follow a symlink.
    (out / "public").mkdir(mode=0o700)
    for suite in SUITES:
        environment = dict(os.environ, **{
            suite["env_prefix"] + "_HOST_CLASS": "native",
            suite["env_prefix"] + "_RESULTS_PATH": str(out / (suite["name"] + ".jsonl")),
            suite["env_prefix"] + "_REPORT_PATH": str(out / (suite["name"] + ".report.json")),
        })
        # SQL URLs, daemon output and raw guest output never enter Actions logs.
        with (out / (suite["name"] + ".private.log")).open("x") as log:
            result = subprocess.run(["make", "GO=" + go, suite["target"]], cwd=REPO, env=environment,
                                    stdout=log, stderr=subprocess.STDOUT, check=False)
        if result.returncode:
            raise ValueError(suite["name"] + "_journey_failed_private_log_retained")
        print("binding native: " + suite["name"] + " journey passed", flush=True)
    if subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip() != source or subprocess.check_output(["git", "status", "--porcelain"], cwd=REPO):
        raise ValueError("source_changed_during_acceptance")
    summary = verify_reports(out, source, publish=True)
    write_report(out / "public" / "summary.json", summary)
    print("binding native: all four guest journeys and cleanup passed; provider/release approval remains separate")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", required=True)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--go", default="go")
    args = parser.parse_args()
    os.umask(0o077)
    try:
        run(args.source, args.out, args.go)
    except (OSError, ValueError, TypeError, KeyError, subprocess.CalledProcessError) as error:
        # Error text is limited to our stable codes, never exception/SQL values.
        code = str(error) if isinstance(error, ValueError) and re.fullmatch(r"[a-z_]+", str(error)) else "acceptance_or_evidence_invalid"
        raise SystemExit("binding native: " + code) from None
