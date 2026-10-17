#!/usr/bin/env python3
"""Reject incomplete storage guest execution; emit bounded qualification evidence."""
import argparse
import datetime as dt
import hashlib
import json
import os
import re
import tempfile
from pathlib import Path

CONTRACT = json.loads((Path(__file__).resolve().parents[2] /
                       "pkg/e2etest/storagestarter/contract.json").read_text())
ROOT, PACKAGE = CONTRACT["root"], CONTRACT["package"]
PHASES = tuple(CONTRACT["phases"])
STARTERS = tuple(CONTRACT["starters"])
IDENTITY_PREFIX = "GREGALE_STORAGE_GUEST_IDENTITY "
MAX_IDENTITY_BYTES = 16384


def timestamp(value):
    if not isinstance(value, str):
        raise ValueError("event_time_invalid")
    result = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if result.utcoffset() != dt.timedelta(0):
        raise ValueError("event_time_invalid")
    return result


def verify(lines):
    children = {ROOT: tuple(f"{ROOT}/{starter['template']}" for starter in STARTERS)}
    ordered = [ROOT]
    for name in children[ROOT]:
        children[name] = tuple(f"{name}/{phase}" for phase in PHASES)
        ordered.extend((name, *children[name]))
    running, passed, identities = [], set(), {}
    started = completed = previous = None
    identity_name, identity_output = None, ""
    next_run = 0
    package_started = package_passed = False
    for line in lines:
        event = json.loads(line)
        if not isinstance(event, dict):
            raise ValueError("event_invalid")
        name, action = event.get("Test", ""), event.get("Action", "")
        if action in ("fail", "build-fail"):
            raise ValueError("execution_failed")
        if event.get("Package") != PACKAGE:
            continue
        if package_passed:
            raise ValueError("events_after_package_pass")
        when = timestamp(event.get("Time"))
        if previous is not None and when < previous:
            raise ValueError("event_time_invalid")
        previous = when
        if action == "start" and not name:
            if package_started:
                raise ValueError("package_start_duplicate")
            package_started, started = True, when
        if not package_started:
            raise ValueError("package_start_missing")
        if name and name not in ordered:
            raise ValueError("unexpected_test")
        if name and action == "skip":
            raise ValueError("phase_skipped")
        if name and action == "run":
            if next_run >= len(ordered) or name != ordered[next_run]:
                raise ValueError("phase_run_invalid")
            parent = name.rpartition("/")[0]
            if parent != (running[-1] if running else ""):
                raise ValueError("phase_order_invalid")
            running.append(name)
            next_run += 1
        elif name and action == "pass":
            if not running or running[-1] != name:
                raise ValueError("phase_pass_without_run")
            if set(children.get(name, ())) - passed:
                raise ValueError("phases_or_cleanup_identity_missing")
            if name in children[ROOT] and name not in identities:
                raise ValueError("cleanup_identity_missing")
            running.pop()
            passed.add(name)
        output = event.get("Output", "")
        if identity_name is not None:
            if action != "output" or name != identity_name or IDENTITY_PREFIX in output:
                raise ValueError("cleanup_identity_interrupted")
            identity_output += output
        elif action == "output" and IDENTITY_PREFIX in output:
            if name not in children[ROOT] or running != [ROOT, name] or set(children[name]) - passed or name in identities:
                raise ValueError("cleanup_identity_invalid")
            identity_name = name
            identity_output = output.split(IDENTITY_PREFIX, 1)[1]
        if identity_name is not None:
            # test2json splits long t.Log lines across Output events (including
            # these artifact manifests). Require one bounded complete log line.
            if len(identity_output.encode("utf-8")) > MAX_IDENTITY_BYTES:
                raise ValueError("cleanup_identity_too_large")
            if "\n" in identity_output:
                identities[identity_name] = json.loads(identity_output.strip())
                identity_name, identity_output = None, ""
        if action == "pass" and not name:
            if running or passed != set(ordered):
                raise ValueError("phases_incomplete")
            package_passed, completed = True, when
    if not package_passed or completed is None or started is None or completed <= started:
        raise ValueError("package_pass_missing")
    return started, completed, identities


def make_report(data, host_class, source_clean, leakcheck_before, leakcheck_after, ttl=86400):
    started, completed, identities = verify(data.decode("utf-8").splitlines())
    if not source_clean or not leakcheck_before or not leakcheck_after:
        raise ValueError("source_or_host_cleanup_unverified")
    if host_class not in ("native", "nested-diagnostic") or type(ttl) is not int or not 1 <= ttl <= 86400:
        raise ValueError("report_parameters_invalid")
    journeys = []
    common = revision = None
    applications, sources = set(), set()
    for starter in STARTERS:
        manifest = identities[f"{ROOT}/{starter['template']}"]
        if not isinstance(manifest, dict) or set(manifest) != {"version", "source_revision", "template", "runtime", "artifacts"}:
            raise ValueError("runtime_identity_invalid")
        if (type(manifest["version"]) is not int or manifest["version"] != CONTRACT["version"]
                or manifest["template"] != starter["template"] or manifest["runtime"] != starter["runtime"]
                or not isinstance(manifest["source_revision"], str) or not re.fullmatch(r"[0-9a-f]{40}", manifest["source_revision"])):
            raise ValueError("runtime_identity_invalid")
        artifacts = manifest["artifacts"]
        if not isinstance(artifacts, dict) or set(artifacts) != {*CONTRACT["artifacts"], starter["app_artifact"], starter["source_artifact"]} or any(
                not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{64}", value) for value in artifacts.values()):
            raise ValueError("runtime_artifacts_invalid")
        shared = {name: artifacts[name] for name in CONTRACT["artifacts"]}
        if common is not None and (common != shared or revision != manifest["source_revision"]):
            raise ValueError("runtime_bundle_mismatch")
        application, source = artifacts[starter["app_artifact"]], artifacts[starter["source_artifact"]]
        if application in applications or source in sources:
            raise ValueError("starter_artifacts_reused")
        applications.add(application)
        sources.add(source)
        common, revision = shared, manifest["source_revision"]
        journeys.append(dict(template=starter["template"], runtime=starter["runtime"], identity=manifest,
                             checks=[dict(name=phase, passed=True) for phase in PHASES]))

    def utc(value):
        return value.isoformat(timespec="seconds").replace("+00:00", "Z")

    report = dict(version=2, suite_version=CONTRACT["version"], root_test=ROOT,
                  journeys=journeys, source_revision=revision, host_class=host_class,
                  native_qualified=host_class == "native", host_os="linux", host_arch="amd64",
                  source_clean=True, started_at=utc(started), completed_at=utc(completed),
                  expires_at=utc(completed + dt.timedelta(seconds=ttl)),
                  leakcheck_before=True, leakcheck_after=True,
                  results_sha256=hashlib.sha256(data).hexdigest())
    canonical = json.dumps(report, sort_keys=True, separators=(",", ":"), allow_nan=False).encode()
    report["evidence_sha256"] = hashlib.sha256(canonical).hexdigest()
    return report


def write_report(path, report):
    # Linking a new private inode refuses both existing files and symlinks.
    descriptor, temporary = tempfile.mkstemp(prefix=".storage-evidence-", dir=Path(path).parent)
    try:
        with os.fdopen(descriptor, "w") as output:
            json.dump(report, output, sort_keys=True, indent=2, allow_nan=False)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.link(temporary, path)
    finally:
        os.unlink(temporary)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("results")
    parser.add_argument("--report", required=True)
    parser.add_argument("--host-class", choices=("native", "nested-diagnostic"), required=True)
    parser.add_argument("--source-clean", action="store_true")
    parser.add_argument("--leakcheck-before", action="store_true")
    parser.add_argument("--leakcheck-after", action="store_true")
    args = parser.parse_args()
    try:
        data = Path(args.results).read_bytes()
        report = make_report(data, args.host_class, args.source_clean, args.leakcheck_before, args.leakcheck_after)
        write_report(args.report, report)
    except (OSError, ValueError, TypeError, KeyError, IndexError) as error:
        raise SystemExit("Storage guest evidence rejected") from error
    print(f"All {len(PHASES)} storage guest phases and cleanup passed for each of {len(STARTERS)} starters ({args.host_class}).")
