#!/usr/bin/env python3
"""Require every queue starter phase and its actual source/runtime bundle."""
import argparse
import datetime as dt
import hashlib
import json
import re
from pathlib import Path

from native_guest_evidence import MAX_IDENTITY_BYTES as MAX_IDENTITY_BYTES
from native_guest_evidence import verify as verify_guest_events
from native_guest_evidence import write_report

CONTRACT = json.loads((Path(__file__).resolve().parents[2] / "pkg/e2etest/queuestarter/contract.json").read_text())
ROOT, PACKAGE, PHASES = CONTRACT["root"], CONTRACT["package"], tuple(CONTRACT["phases"])
IDENTITY_PREFIX = "GREGALE_QUEUE_GUEST_IDENTITY "


def verify(lines):
    started, completed, manifest = verify_guest_events(lines, CONTRACT, IDENTITY_PREFIX)
    if not isinstance(manifest, dict) or set(manifest) != {"version", "source_revision", "template", "runtime", "artifacts"}:
        raise ValueError("queue_identity_invalid")
    if (type(manifest["version"]) is not int or manifest["version"] != CONTRACT["version"]
            or manifest["template"] != CONTRACT["template"] or manifest["runtime"] != CONTRACT["runtime"]
            or not isinstance(manifest["source_revision"], str) or not re.fullmatch(r"[0-9a-f]{40}", manifest["source_revision"])):
        raise ValueError("queue_runtime_invalid")
    artifacts = manifest["artifacts"]
    if (not isinstance(artifacts, dict) or set(artifacts) != set(CONTRACT["artifacts"])
            or any(not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{64}", value) for value in artifacts.values())):
        raise ValueError("queue_artifacts_invalid")
    return started, completed, manifest


def make_report(data, host_class, source_clean, before, after, ttl=86400):
    started, completed, manifest = verify(data.decode("utf-8").splitlines())
    if (host_class not in ("native", "nested-diagnostic") or source_clean is not True
            or before is not True or after is not True or type(ttl) is not int or not 1 <= ttl <= 7*86400):
        raise ValueError("queue_qualification_parameters_invalid")
    def utc(value):
        return value.astimezone(dt.UTC).isoformat(timespec="seconds").replace("+00:00", "Z")
    report = dict(version=CONTRACT["version"], suite_version=CONTRACT["version"], root_test=ROOT,
                  host_class=host_class, native_qualified=host_class == "native", host_os="linux", host_arch="amd64",
                  source_clean=True, leakcheck_before=True, leakcheck_after=True,
                  started_at=utc(started), completed_at=utc(completed), expires_at=utc(completed+dt.timedelta(seconds=ttl)),
                  runtime=manifest, checks=[dict(name=phase, passed=True) for phase in PHASES],
                  results_sha256=hashlib.sha256(data).hexdigest())
    report["evidence_sha256"] = hashlib.sha256(json.dumps(report, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()).hexdigest()
    return report


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("results")
    parser.add_argument("--report")
    parser.add_argument("--host-class", choices=("native", "nested-diagnostic"))
    parser.add_argument("--source-clean", action="store_true")
    parser.add_argument("--leakcheck-before", action="store_true")
    parser.add_argument("--leakcheck-after", action="store_true")
    args = parser.parse_args()
    try:
        data = Path(args.results).read_bytes()
        if args.report:
            write_report(args.report, make_report(data, args.host_class, args.source_clean, args.leakcheck_before, args.leakcheck_after))
        else:
            verify(data.decode("utf-8").splitlines())
    except (OSError, ValueError, TypeError, KeyError) as error:
        raise SystemExit("Queue guest evidence rejected") from error
    print(f"All {len(PHASES)} queue starter, completion, recovery, release-fencing and cleanup phases passed.")
