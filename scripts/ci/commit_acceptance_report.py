#!/usr/bin/env python3
"""Run the Commit gate and retain only non-sensitive CI evidence."""

import hashlib
import json
import re
import subprocess
import sys
import tempfile
import time
from pathlib import Path


GATES = {
    "process": ("test-commit", "PostgreSQL and Linux process acceptance; native KVM qualification is separate"),
    "consumer-load": ("test-commit-consumer-load", "Sustained Linux consumer acceptance; native KVM and production qualification are separate"),
}


def write_report(path, report):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")


def prepare(path, gate="process"):
    if subprocess.check_output(["git", "status", "--porcelain", "--untracked-files=all"]):
        raise RuntimeError("The checkout must be clean before recording its source identity")
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    digest = hashlib.sha256()
    with tempfile.TemporaryFile() as archive:
        subprocess.run(["git", "archive", "--format=tar", "HEAD"], stdout=archive, check=True)
        archive.seek(0)
        for chunk in iter(lambda: archive.read(1024 * 1024), b""):
            digest.update(chunk)
    command, scope = GATES[gate]
    write_report(path, {
        "schema_version": 1,
        "source_revision": revision,
        "source_archive_sha256": digest.hexdigest(),
        "gate": "make " + command,
        "status": "not_started",
        "scope": scope,
    })


def failure_counts(output):
    counts = {"failed_tests": 0, "skipped_tests": 0, "failed_packages": 0}
    found = False
    for line in output:
        try:
            event = json.loads(line)
        except (json.JSONDecodeError, UnicodeDecodeError):
            continue
        if not isinstance(event, dict) or event.get("Action") not in ("fail", "skip"):
            continue
        found = True
        if event.get("Test"):
            counts["failed_tests" if event["Action"] == "fail" else "skipped_tests"] += 1
        elif event["Action"] == "fail":
            counts["failed_packages"] += 1
    return counts if found else None


def sdk_ran_without_skips(output):
    # Node's TAP summary and Python unittest's final OK must both be present.
    # The Go gate independently verifies its own required tests and skips.
    node = {}
    python_tests = 0
    python_ok = False
    for line in output:
        match = re.fullmatch(r"# (tests|pass|skipped|todo) (\d+)\s*", line)
        if match:
            node[match.group(1)] = int(match.group(2))
        match = re.fullmatch(r"Ran (\d+) tests? in .*\s*", line)
        if match:
            python_tests = int(match.group(1))
        if line.strip() == "OK":
            python_ok = True
    return (node.get("tests", 0) > 0 and node.get("pass") == node["tests"]
            and node.get("skipped") == 0 and node.get("todo") == 0
            and python_tests > 0 and python_ok)


def consumer_load_summary(output):
    summaries = []
    pattern = re.compile(r"\bsustained Commit consumer load passed: duration=(\d+)m(\d+)(?:\.(\d{1,9}))?s rate=([1-5]) events=(\d+)\s*$")
    for line in output:
        match = pattern.search(line)
        if match:
            minutes, seconds, fraction, rate, events = match.groups()
            minutes, seconds, rate, events = map(int, (minutes, seconds, rate, events))
            duration = minutes * 60 + seconds + (int(fraction) / 10 ** len(fraction) if fraction else 0)
            if seconds < 60 and 65 <= duration <= 300 and 5 <= events <= duration * rate + 3:
                summaries.append({"configured_duration_seconds": duration, "target_events_per_second": rate, "verified_events": events})
            else:
                return None
    return summaries[0] if len(summaries) == 1 else None


def run(path, gate="process"):
    report = json.loads(path.read_text())
    command, _ = GATES[gate]
    if report.get("gate") != "make " + command:
        raise ValueError("Report gate does not match the requested acceptance gate")
    report["status"] = "running"
    write_report(path, report)
    start = time.monotonic()
    # Raw logs may contain URLs or credentials. Never publish them as artifacts.
    with tempfile.TemporaryFile(mode="w+t", encoding="utf-8", errors="replace") as output:
        result = subprocess.run(["make", command], stdout=output, stderr=subprocess.STDOUT, check=False)
        output.seek(0)
        counts = failure_counts(output)
        output.seek(0)
        if gate == "process":
            check = sdk_ran_without_skips(output) if result.returncode == 0 else None
        else:
            check = consumer_load_summary(output) if result.returncode == 0 else None
    passed = result.returncode == 0 and bool(check) and (gate != "consumer-load" or counts is None)
    report["status"] = "passed" if passed else "failed"
    report["gate_exit_code"] = result.returncode
    check_name = "sdk_skip_check" if gate == "process" else "load_summary_check"
    report[check_name] = ("passed" if check else "failed") if result.returncode == 0 else "not_checked"
    if gate == "consumer-load" and passed:
        report["consumer_load"] = check
    report["elapsed_seconds"] = round(time.monotonic() - start, 2)
    if counts is not None:
        report["failure_event_counts"] = counts
    write_report(path, report)
    print("Commit acceptance " + report["status"] + ". Raw output is not included in CI evidence.")
    return 0 if passed else (result.returncode or 1)


def main():
    if len(sys.argv) not in (3, 4) or sys.argv[1] not in ("prepare", "run") or (len(sys.argv) == 4 and sys.argv[3] not in GATES):
        raise SystemExit("Usage: commit_acceptance_report.py {prepare|run} REPORT_PATH [process|consumer-load]")
    path = Path(sys.argv[2])
    gate = sys.argv[3] if len(sys.argv) == 4 else "process"
    if sys.argv[1] == "prepare":
        prepare(path, gate)
        return 0
    return run(path, gate)


if __name__ == "__main__":
    sys.exit(main())
