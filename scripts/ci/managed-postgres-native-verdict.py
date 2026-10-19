#!/usr/bin/env python3
"""Verify guest execution and optionally emit a bounded, non-secret report."""
import argparse
import datetime as dt
import hashlib
import json
import re
from pathlib import Path

from native_guest_evidence import MAX_IDENTITY_BYTES as MAX_IDENTITY_BYTES
from native_guest_evidence import verify as verify_guest_events
from native_guest_evidence import write_report

CONTRACT = json.loads((Path(__file__).resolve().parents[2] /
                       "pkg/managedpostgres/guestqualification/contract.json").read_text())
ROOT = CONTRACT["root"]
PHASES = tuple(CONTRACT["phases"])
PACKAGE = CONTRACT["package"]
IDENTITY_PREFIX = "GREGALE_POSTGRES_GUEST_IDENTITY "


def verify(lines):
    return verify_guest_events(lines, CONTRACT, IDENTITY_PREFIX)


def make_report(data, host_class, source_clean, leakcheck_before, leakcheck_after, ttl):
    started, completed, identity = verify(data.decode("utf-8").splitlines())
    if not isinstance(identity, dict) or set(identity) != {"runtime", "postgres_major"}:
        raise ValueError("guest_identity_missing_or_invalid")
    manifest = identity["runtime"]
    if not isinstance(manifest, dict) or set(manifest) != {"version", "source_revision", "artifacts"}:
        raise ValueError("guest_runtime_invalid")
    if (type(manifest["version"]) is not int or manifest["version"] != CONTRACT["version"]
            or not isinstance(manifest["source_revision"], str) or not re.fullmatch(r"[0-9a-f]{40}", manifest["source_revision"])):
        raise ValueError("guest_runtime_invalid")
    artifacts = manifest["artifacts"]
    if not isinstance(artifacts, dict) or set(artifacts) != set(CONTRACT["artifacts"]) or any(
            not isinstance(v, str) or not re.fullmatch(r"[0-9a-f]{64}", v) for v in artifacts.values()):
        raise ValueError("guest_runtime_invalid")
    major = identity["postgres_major"]
    applications = [artifacts[workload + "_app_layer"] for workload in CONTRACT["workloads"]]
    sources = [artifacts[workload + "_source_archive"] for workload in CONTRACT["workloads"]]
    if len(set(applications)) != len(applications) or len(set(sources)) != len(sources):
        raise ValueError("guest_workload_artifacts_reused")
    if (type(major) is not int or not 14 <= major <= 18 or type(ttl) is not int or not 1 <= ttl <= 7 * 86400
            or host_class not in ("native", "nested-diagnostic") or source_clean is not True):
        raise ValueError("guest_report_parameters_invalid")
    if not leakcheck_before or not leakcheck_after:
        raise ValueError("guest_host_cleanup_unverified")
    def utc(value):
        return value.astimezone(dt.UTC).isoformat(timespec="seconds").replace("+00:00", "Z")
    report = dict(version=CONTRACT["version"], suite_version=CONTRACT["version"], root_test=ROOT,
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
            write_report(args.report, report)
        else:
            verify(data.decode("utf-8").splitlines())
    except (OSError, ValueError, TypeError, KeyError) as error:
        raise SystemExit("PostgreSQL guest evidence rejected") from error
    print(f"All {len(PHASES)} PostgreSQL probe, Node/Python starter, public rotation, restore, cold-boot and cleanup phases passed.")
