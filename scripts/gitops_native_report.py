#!/usr/bin/env python3
"""Private, fail-closed evidence for the existing native GitOps journey.

This is a local operator record, not signed attestation or production authority.
Only structured Go test events are interpreted; raw log output stays private.
"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import stat
import subprocess
import tempfile

PACKAGE = "github.com/onebox-faas/faas/cmd/e2e"
ROOT_TEST = "TestEnvironmentGitOpsNativeMetal"
SOURCE_JOB_TEST = "TestEnvironmentGitOpsSourceJobsNativeMetal"
SCHEMA = "gregale.dev/gitops-native-acceptance/v2"
SOURCE_KINDS = ("source", "dockerfile")
SOURCE_JOB_SCENARIOS = ("qualified_schedule", "artifact_reuse", "scheduler_restart",
                        "held_build_supersession", "reviewed_rollback")
FIXTURES = ("kernel", "guest_init")
BASE_IMAGES = {"builder": "GREGALE_GITOPS_NATIVE_BUILDER_REF",
               "source_runtime": "GREGALE_GITOPS_NATIVE_SOURCE_RUNTIME_REF",
               "dockerfile_runtime": "GREGALE_GITOPS_NATIVE_DOCKERFILE_RUNTIME_REF"}


def pinned_image(value):
    # Credentials, URLs and arbitrary operator strings never enter report JSON.
    return value if isinstance(value, str) and "://" not in value and re.fullmatch(r"[a-z0-9][a-z0-9./:_-]*@sha256:[a-f0-9]{64}", value) else None
PROVIDERS = ("github", "gitlab")
SCENARIOS = (
    "baseline_serving", "source_outage_restart", "held_revision_supersession",
    "scheduler_restart_inflight", "reviewed_rollback", "override_repair",
    "retirement_recovery",
)
ARTIFACTS = ("test-events.jsonl", "runner.log", "leak-before.log", "leak-after.log", "postgres-probe.log")


def utc_now():
    return datetime.now(timezone.utc).isoformat()


def command(args, cwd=None, valid_codes=(0,)):
    try:
        result = subprocess.run(args, cwd=cwd, capture_output=True, timeout=10, check=False)
        if result.returncode in valid_codes and len(result.stdout) <= 4096:
            return result.stdout.decode("utf-8").strip()
    except (OSError, subprocess.TimeoutExpired, UnicodeError):
        pass
    return None


def fingerprint(path):
    """Do not include fixture paths or contents in the shareable JSON."""
    if not path:
        return None
    try:
        digest = hashlib.sha256()
        size = 0
        # A bad fixture path must not block on a FIFO/device or hash an
        # unbounded stream before host preflight has had a chance to reject it.
        fd = os.open(path, os.O_RDONLY | os.O_NONBLOCK)
        with os.fdopen(fd, "rb") as stream:
            before = os.fstat(stream.fileno())
            if not stat.S_ISREG(before.st_mode):
                return None
            while size < before.st_size:
                chunk = stream.read(min(1 << 20, before.st_size - size))
                if not chunk:
                    return None
                digest.update(chunk)
                size += len(chunk)
            after = os.fstat(stream.fileno())
            if stream.read(1) or (before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (
                after.st_size, after.st_mtime_ns, after.st_ctime_ns
            ):
                return None
        return {"sha256": digest.hexdigest(), "bytes": size}
    except OSError:
        return None


def snapshot(repo):
    status = command(["git", "status", "--porcelain"], repo)
    revision = command(["git", "rev-parse", "HEAD"], repo)
    return {"commit": revision, "clean": status == ""}


def metadata(repo):
    go_mod = (repo / "go.mod").read_text()
    pinned = re.search(r"^go ([0-9.]+)$", go_mod, re.MULTILINE)
    return {
        "source": snapshot(repo),
        "host": {"name": platform.node(), "system": platform.system(),
                 "architecture": platform.machine(), "kernel_release": platform.release(),
                 "virtualization_probe": command(["systemd-detect-virt", "--vm"], valid_codes=(0, 1))},
        "versions": {"go": command([os.environ.get("GO", "go"), "env", "GOVERSION"]),
                     "go_required": "go" + pinned[1] if pinned else None,
                     "firecracker": command(["firecracker", "--version"]),
                     "jailer": command(["jailer", "--version"]),
                     "postgres_client": command(["psql", "--version"])},
        "base_images": {key: pinned_image(os.environ.get(variable)) for key, variable in BASE_IMAGES.items()},
        "fixtures": {"kernel": fingerprint(os.environ.get("FAAS_TEST_KERNEL")),
                     "guest_init": fingerprint(os.environ.get("FAAS_GUEST_INIT"))},
    }


def required_tests():
    names = [ROOT_TEST]
    for provider in PROVIDERS:
        names.append(f"{ROOT_TEST}/{provider}")
        names.extend(f"{ROOT_TEST}/{provider}/{scenario}" for scenario in SCENARIOS)
    names.append(SOURCE_JOB_TEST)
    for provider in PROVIDERS:
        parent = f"{SOURCE_JOB_TEST}/{provider}"
        names.append(parent)
        for kind in SOURCE_KINDS:
            case = f"{parent}/{kind}"
            names.append(case)
            names.extend(f"{case}/{scenario}" for scenario in SOURCE_JOB_SCENARIOS)
    return names


def required_coverage():
    return {"providers": list(PROVIDERS), "provider_boundary": "local_fixture",
            "approval_policy": "manual", "workloads": ["image_api", "image_push_worker", "source_scheduled_job", "dockerfile_scheduled_job"],
            "source_kinds": list(SOURCE_KINDS), "source_build_execution": "builder_microvm",
            "live_provider_access": False, "protected_review_acceptance": False}


def read_events(path):
    expected = required_tests()
    states = {name: "not_run" for name in expected}
    running, terminal = set(), set()
    package_started, package_result, invalid = False, "not_run", False
    try:
        with path.open("rb") as stream:
            while line := stream.readline((1 << 20) + 1):
                if len(line) > 1 << 20:
                    invalid = True
                    break
                try:
                    event = json.loads(line)
                    if not isinstance(event, dict):
                        raise ValueError("event must be an object")
                except (ValueError, UnicodeError):
                    invalid = True
                    continue
                if event.get("Package") != PACKAGE:
                    if event.get("Action") in ("fail", "skip"):
                        invalid = True
                    continue
                action, test = event.get("Action"), event.get("Test")
                if not test:
                    if action == "start":
                        if package_started or package_result != "not_run":
                            invalid = True
                        package_started = True
                    elif action in ("pass", "fail", "skip"):
                        if not package_started or package_result != "not_run":
                            invalid = True
                        if action == "pass" and any(v != "pass" for v in states.values()):
                            invalid = True
                        package_result = action
                    continue
                if test not in states:
                    # Extra tests cannot substitute for a required scenario.
                    if action in ("fail", "skip"):
                        invalid = True
                    continue
                if not package_started or package_result != "not_run":
                    invalid = True
                if action == "run":
                    if test in running or test in terminal:
                        invalid = True
                    if "/" in test:
                        parent = test.rsplit("/", 1)[0]
                        if parent not in running or parent in terminal:
                            invalid = True
                    running.add(test)
                    states[test] = "running"
                elif action in ("pass", "fail", "skip"):
                    if test not in running or test in terminal:
                        invalid = True
                    if action == "pass" and any(
                        states[name] != "pass" for name in expected if name.startswith(test + "/")
                    ):
                        invalid = True
                    terminal.add(test)
                    states[test] = action
    except OSError:
        invalid = True
    complete = not invalid and package_result == "pass" and all(v == "pass" for v in states.values())
    return {"valid_stream": not invalid, "package_result": package_result,
            "complete": complete, "tests": states}


def write_json(path, value):
    # The directory is private and newly created by init; replace atomically.
    fd, name = tempfile.mkstemp(prefix=".report-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream, indent=2, sort_keys=True)
            stream.write("\n")
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def initialize(repo, requested, host_class):
    repo = repo.resolve()
    if requested:
        directory = Path(requested).absolute()
        if directory.resolve().is_relative_to(repo):
            raise ValueError("report directory must be outside the source checkout")
        directory.mkdir(mode=0o700)  # Never overwrite/reuse an earlier run.
    else:
        directory = Path(tempfile.mkdtemp(prefix="gregale-gitops-native."))
        if directory.resolve().is_relative_to(repo):
            directory.rmdir()
            raise ValueError("report directory must be outside the source checkout")
    os.chmod(directory, 0o700)
    report = {"schema": SCHEMA, "started_at": utc_now(),
              "host_class": host_class, "evidence_kind": "local_operator_run",
              "production_enforcement_enabled": False, "native_acceptance_passed": False,
              "source_scheduled_jobs_passed": False,
              "coverage": required_coverage(),
              "initial": metadata(repo), "result": "incomplete"}
    write_json(directory / "report.json", report)
    return directory


def assess(report, final, stages, reason, exit_code, events):
    """Recompute the verdict from observed evidence, without trusting flags."""
    initial = report["initial"]
    identity_stable = initial == final and initial["source"]["clean"] and bool(
        re.fullmatch(r"[a-f0-9]{40}", initial["source"]["commit"] or ""))
    versions = initial["versions"]
    contract_current = report.get("schema") == SCHEMA and report.get("coverage") == required_coverage()
    metadata_complete = (set(initial.get("base_images", {})) == set(BASE_IMAGES) and
                         all(pinned_image(initial["base_images"].get(key)) for key in BASE_IMAGES) and
                         set(initial["fixtures"]) == set(FIXTURES) and
                         all(initial["fixtures"].get(key) and initial["fixtures"][key]["bytes"] > 0 for key in FIXTURES) and
                         all(versions.get(key) for key in ("go", "firecracker", "jailer", "postgres_client")) and
                         versions["go"] == versions["go_required"])
    native = (report["host_class"] == "native" and initial["host"]["system"] == "Linux" and
              initial["host"]["architecture"] == "x86_64" and initial["host"]["virtualization_probe"] == "none")
    supported = (native or report["host_class"] == "nested-diagnostic" and
                 initial["host"]["system"] == "Linux" and initial["host"]["architecture"] == "x86_64" and
                 bool(initial["host"]["virtualization_probe"]))
    passed = (contract_current and supported and exit_code == 0 and identity_stable and metadata_complete and events["complete"] and
              all(stages.get(key) == "pass" for key in ("preflight", "leak_before", "journey", "leak_after", "source")))
    if passed:
        result = "passed" if native else "diagnostic_passed"
    else:
        result = "blocked" if stages.get("preflight") != "pass" else "failed"
    failures = [reason] if reason else []
    failures.extend(key + "_" + value for key, value in stages.items() if value != "pass")
    if not contract_current:
        failures.append("report_contract_mismatch")
    if not identity_stable:
        failures.append("source_or_inputs_changed_or_unclean")
    if not metadata_complete:
        failures.append("missing_or_unpinned_runtime_metadata")
    if not supported:
        failures.append("unsupported_or_unverified_host")
    if not events["complete"]:
        failures.append("required_test_matrix_incomplete")
    return dict(final=final, source_and_inputs_unchanged=identity_stable,
                metadata_complete=bool(metadata_complete), stages=stages, failure_reasons=failures,
                command_exit_code=exit_code, exit_code=exit_code or (0 if passed else 1), events=events, result=result,
                native_acceptance_passed=bool(passed and native), source_scheduled_jobs_passed=bool(passed and native))


def finish(repo, directory, stages, reason, exit_code):
    path = directory / "report.json"
    report = json.loads(path.read_text())
    observed = assess(report, metadata(repo), stages, reason, exit_code, read_events(directory / "test-events.jsonl"))
    report.update(finished_at=utc_now(), **observed,
                  artifacts={name: fingerprint(directory / name) for name in ARTIFACTS})
    write_json(path, report)
    return observed["exit_code"] == 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("init", "finish"))
    parser.add_argument("--repo", required=True, type=Path)
    parser.add_argument("--directory", default="")
    parser.add_argument("--host-class", default="")
    parser.add_argument("--exit-code", default=1, type=int)
    parser.add_argument("--reason", default="")
    for name in ("preflight", "leak-before", "journey", "leak-after", "source"):
        parser.add_argument("--" + name, default="not_run", choices=("not_run", "pass", "fail", "blocked"))
    args = parser.parse_args()
    if args.operation == "init":
        print(initialize(args.repo, args.directory, args.host_class))
        return 0
    stages = {key: getattr(args, key) for key in ("preflight", "leak_before", "journey", "leak_after", "source")}
    return 0 if finish(args.repo, Path(args.directory), stages, args.reason, args.exit_code) else 1


if __name__ == "__main__":
    raise SystemExit(main())
