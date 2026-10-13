#!/usr/bin/env python3
"""Verify guest execution and optionally emit a bounded, non-secret report."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import tempfile

CONTRACT = json.loads((Path(__file__).resolve().parents[2] /
                       "pkg/managedpostgres/guestqualification/contract.json").read_text())
ROOT = CONTRACT["root"]
PHASES = tuple(CONTRACT["phases"])
PACKAGE = CONTRACT["package"]
IDENTITY_PREFIX = "GREGALE_POSTGRES_GUEST_IDENTITY "


def timestamp(value):
    if not isinstance(value, str):
        raise ValueError("guest_event_time_invalid")
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.utcoffset() != dt.timedelta(0):
        raise ValueError("guest_event_time_invalid")
    return parsed


def verify(lines):
    required = {ROOT, *(f"{ROOT}/{phase}" for phase in PHASES)}
    running, passed = set(), set()
    started = completed = identity = None
    package_started = package_passed = False
    for line in lines:
        event = json.loads(line)
        if not isinstance(event, dict):
            raise ValueError("guest_event_invalid")
        if event.get("Package") != PACKAGE:
            if event.get("Action") in ("fail", "build-fail"):
                raise ValueError("guest_package_failed")
            continue
        if package_passed:
            raise ValueError("guest_events_after_package_pass")
        name, action = event.get("Test", ""), event.get("Action", "")
        when = timestamp(event.get("Time"))
        if started is not None and when < started:
            raise ValueError("guest_event_time_invalid")
        if action == "start" and not name:
            if package_started:
                raise ValueError("guest_package_start_duplicate")
            package_started, started = True, when
        if not package_started:
            raise ValueError("guest_package_start_missing")
        if action in ("fail", "build-fail") or ((name == ROOT or name.startswith(ROOT + "/")) and action == "skip"):
            raise ValueError("guest_test_failed_or_skipped")
        if name in required:
            if action == "run":
                if name in running or name in passed or (name != ROOT and ROOT not in running):
                    raise ValueError("guest_test_run_invalid")
                running.add(name)
            elif action == "pass":
                if name == ROOT and required - {ROOT} - passed:
                    raise ValueError("guest_phases_incomplete: " + ", ".join(sorted(required - {ROOT} - passed)))
                if name not in running:
                    raise ValueError("guest_test_pass_invalid")
                running.remove(name)
                passed.add(name)
        if name == ROOT and action == "output" and IDENTITY_PREFIX in event.get("Output", ""):
            if identity is not None:
                raise ValueError("guest_identity_duplicate")
            identity = json.loads(event["Output"].split(IDENTITY_PREFIX, 1)[1].strip())
        if action == "pass" and not name:
            if running or required - passed:
                raise ValueError("guest_phases_incomplete: " + ", ".join(sorted(required - passed)))
            package_passed, completed = True, when
    missing = required - passed
    if missing:
        raise ValueError("guest_phases_incomplete: " + ", ".join(sorted(missing)))
    if not package_passed or not started or not completed or completed <= started:
        raise ValueError("guest_package_pass_missing")
    return started, completed, identity


def make_report(data, host_class, source_clean, leakcheck_before, leakcheck_after, ttl):
    started, completed, identity = verify(data.decode("utf-8").splitlines())
    if not isinstance(identity, dict) or set(identity) != {"runtime", "postgres_major"}:
        raise ValueError("guest_identity_missing_or_invalid")
    manifest = identity["runtime"]
    if not isinstance(manifest, dict) or set(manifest) != {"version", "source_revision", "artifacts"}:
        raise ValueError("guest_runtime_invalid")
    if manifest["version"] != 1 or not re.fullmatch(r"[0-9a-f]{40}", manifest["source_revision"]):
        raise ValueError("guest_runtime_invalid")
    artifacts = manifest["artifacts"]
    if not isinstance(artifacts, dict) or set(artifacts) != set(CONTRACT["artifacts"]) or any(
            not isinstance(v, str) or not re.fullmatch(r"[0-9a-f]{64}", v) for v in artifacts.values()):
        raise ValueError("guest_runtime_invalid")
    major = identity["postgres_major"]
    if type(major) is not int or not 14 <= major <= 18 or not 1 <= ttl <= 7 * 86400:
        raise ValueError("guest_report_parameters_invalid")
    if not leakcheck_before or not leakcheck_after:
        raise ValueError("guest_host_cleanup_unverified")
    def utc(value):
        return value.astimezone(dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")
    report = dict(version=1, suite_version=CONTRACT["version"], root_test=ROOT,
                  host_class=host_class, host_os="linux", host_arch="amd64",
                  source_clean=source_clean, postgres_major=major,
                  started_at=utc(started), completed_at=utc(completed),
                  expires_at=utc(completed + dt.timedelta(seconds=ttl)), runtime=manifest,
                  checks=[dict(name=p, passed=True) for p in PHASES],
                  leakcheck_before=True, leakcheck_after=True,
                  results_sha256=hashlib.sha256(data).hexdigest())
    canonical = json.dumps(report, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()
    report["evidence_sha256"] = hashlib.sha256(canonical).hexdigest()
    return report


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("results")
    parser.add_argument("--report")
    parser.add_argument("--host-class", choices=("native", "nested-diagnostic"))
    parser.add_argument("--source-clean", action="store_true")
    parser.add_argument("--leakcheck-before", action="store_true")
    parser.add_argument("--leakcheck-after", action="store_true")
    parser.add_argument("--ttl-seconds", type=int, default=86400)
    args = parser.parse_args()
    try:
        data = Path(args.results).read_bytes()
        if args.report:
            if args.host_class is None:
                raise ValueError("guest_host_class_missing")
            report = make_report(data, args.host_class, args.source_clean,
                                 args.leakcheck_before, args.leakcheck_after, args.ttl_seconds)
            # Refuse to replace older evidence after a failed/repeated run.
            descriptor, temporary = tempfile.mkstemp(prefix=".guest-evidence-", dir=Path(args.report).parent)
            try:
                with os.fdopen(descriptor, "w") as output:
                    json.dump(report, output, sort_keys=True, indent=2, allow_nan=False)
                    output.write("\n")
                    output.flush()
                    os.fsync(output.fileno())
                os.link(temporary, args.report)
            finally:
                os.unlink(temporary)
        else:
            verify(data.decode("utf-8").splitlines())
    except (OSError, ValueError, TypeError, KeyError) as error:
        raise SystemExit("PostgreSQL guest evidence rejected") from error
    print("All thirteen PostgreSQL probe, shipped starter, public rotation, restore, cold-boot and cleanup phases passed.")
