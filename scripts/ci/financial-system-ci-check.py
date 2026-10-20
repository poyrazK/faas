#!/usr/bin/env python3
"""Validate collected financial evidence before CI removes the host staging."""
import argparse
import importlib.util
import json
from pathlib import Path
import sys

spec = importlib.util.spec_from_file_location("financial_verdict", Path(__file__).with_name("financial-system-verdict.py"))
verdict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verdict)


def verify(root, directory, source_sha, run_id):
    report = json.loads((directory / f"financial-system-verdict-{run_id}.json").read_text())
    if not isinstance(report, dict):
        raise ValueError("collected verdict is not an evidence object")
    if report.get("qualified") is not True or report.get("source_sha") != source_sha or report.get("run_id") != run_id:
        raise ValueError("matching full-system financial verdict did not qualify")
    if report.get("scope") != "single_host_full_daemon_and_provider_replay" or report.get("failures"):
        raise ValueError("financial verdict has the wrong scope or contains failures")
    for lane in verdict.SOURCES:
        record = report["lanes"][lane]
        path = directory / Path(record["log"]).name
        checked, errors = verdict.inspect_lane(root, lane, path, record["exit_code"])
        if errors or checked["log_sha256"] != record["log_sha256"] or checked["source_sha256"] != record["source_sha256"]:
            raise ValueError(f"{lane}: collected log/source does not match the native verdict")
        if lane == "native" and checked.get("remote_http") != record.get("remote_http"):
            raise ValueError("remote HTTP measurements or supporting sources changed")
    proofs = verdict.reconciliation_contract(root, directory / "financial-system-records")
    if {key: value["sha256"] for key, value in proofs.items()} != {key: value["sha256"] for key, value in report["reconciliation"].items()}:
        raise ValueError("collected provider proof hashes changed")
    if any(type(report["checks"].get(key)) is not int or report["checks"][key] != 0 for key in ("runner", "leakcheck", "service_restoration")):
        raise ValueError("native cleanup or restoration did not pass")
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--run-id", required=True)
    args = parser.parse_args()
    try:
        verify(args.root, args.evidence, args.source_sha, args.run_id)
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(f"financial CI evidence: FAIL: {error}", file=sys.stderr)
        return 1
    print("financial CI evidence: PASS (full-system lane; remaining release gates still apply)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
