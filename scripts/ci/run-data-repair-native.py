#!/usr/bin/env python3
"""Run designated-host repair qualification and retain its evidence on failure."""
from __future__ import annotations

import argparse
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import uuid
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = "github.com/onebox-faas/faas/pkg/fcvm"
TESTS = (
    "TestMetalDataRepairRehearsal",
    "TestMetalDataRepairProducerRecovery",
    "TestMetalDataRepairCrashRecovery",
    "TestDataRepairCrashResultWitness",
)
CRASH = "TestMetalDataRepairCrashRecovery"
PHASES = ("boot", "rehearsal", "cleanup", "cleanup_ack")


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def sha256(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def write_json(path, value):
    with path.open("x", encoding="utf-8") as stream:
        json.dump(value, stream, sort_keys=True, indent=2)
        stream.write("\n")
    path.chmod(0o600)


def capture(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True, stderr=subprocess.DEVNULL).strip()


def source_identity():
    # Include uncommitted files: HEAD alone would identify the wrong code here.
    paths = subprocess.check_output(
        ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"], cwd=ROOT
    ).decode().split("\0")
    files = []
    for name in sorted(set(paths) - {""}):
        path = ROOT / name
        if not path.exists() and not path.is_symlink():
            files.append(dict(path=name, deleted=True))
            continue
        mode = path.lstat().st_mode
        if stat.S_ISLNK(mode):
            digest = hashlib.sha256(os.fsencode(os.readlink(path))).hexdigest()
        elif stat.S_ISREG(mode):
            digest = sha256(path)
        else:
            raise ValueError("unsupported source file type")
        files.append(dict(path=name, mode=stat.S_IMODE(mode), sha256=digest))
    digest = hashlib.sha256(json.dumps(files, sort_keys=True).encode()).hexdigest()
    return dict(head=capture("git", "rev-parse", "HEAD"),
                dirty=bool(capture("git", "status", "--porcelain", "--untracked-files=normal")),
                tree_sha256=digest, files=files)


def inspect_host(env):
    errors, host, assets = [], {}, {}
    if platform.system() != "Linux" or platform.machine() != "x86_64":
        errors.append("native Linux x86_64 required")
    if os.geteuid() != 0:
        errors.append("root required")
    if not Path("/etc/faas/builder-acceptance-host").is_file():
        errors.append("designated acceptance host marker absent")
    for name in ("/dev/kvm", "/dev/loop-control"):
        path = Path(name)
        if not path.exists() or not stat.S_ISCHR(path.stat().st_mode):
            errors.append(f"character device missing: {name}")
    if not Path("/sys/fs/cgroup/cgroup.controllers").is_file():
        errors.append("cgroups v2 required")
    for tool in ("make", "firecracker", "jailer", "unshare", "ip", "losetup", "mount", "umount", "systemctl", "systemd-detect-virt"):
        if not shutil.which(tool):
            errors.append(f"host tool missing: {tool}")
    if shutil.which("systemd-detect-virt"):
        result = subprocess.run(["systemd-detect-virt"], capture_output=True, text=True, check=False)
        if result.returncode != 1 or result.stdout.strip() != "none":
            errors.append("virtualized or unverified host")
    if shutil.which("systemctl"):
        for service in ("faas-vmmd", "faas-builderd", "faas-imaged", "faas-schedd", "faas-gatewayd-internal", "faas-gatewayd-public"):
            result = subprocess.run(["systemctl", "is-active", "--quiet", service], capture_output=True, check=False)
            if result.returncode not in (3, 4):
                errors.append(f"service active or state unavailable: {service}")
    for label, name in (("machine_id", "/etc/machine-id"), ("boot_id", "/proc/sys/kernel/random/boot_id")):
        try:
            identity = uuid.UUID(Path(name).read_text().strip())
            if identity.int == 0:
                raise ValueError("zero identity")
            host[label] = str(identity)
        except (OSError, ValueError):
            errors.append(f"invalid {label}")
    if urlsplit(env.get("DATABASE_URL", "")).scheme not in ("postgres", "postgresql"):
        errors.append("disposable PostgreSQL 16 DATABASE_URL required")
    go = shutil.which(env.get("GO", "go"))
    if not go:
        errors.append("Go executable missing")
    else:
        host["go"] = go
        module = (ROOT / "go.mod").read_text()
        compiler = re.search(r"^toolchain (go\S+)$", module, re.M)
        expected = compiler.group(1) if compiler else "go" + re.search(r"^go (\S+)$", module, re.M).group(1)
        try:
            host["go_version"] = capture(go, "env", "GOVERSION")
            if host["go_version"] != expected:
                errors.append("Go version differs from the pinned go.mod compiler")
        except subprocess.CalledProcessError:
            errors.append("Go version unavailable")
    for label, variable, hash_variable in (
        ("kernel", "FAAS_TEST_KERNEL", "FAAS_DATA_REPAIR_RUNNER_KERNEL_SHA256"),
        ("base", "FAAS_TEST_BASE_ROOTFS", "FAAS_DATA_REPAIR_RUNNER_BASE_SHA256"),
        ("layer", "FAAS_TEST_LAYER_ROOTFS", "FAAS_DATA_REPAIR_RUNNER_LAYER_SHA256"),
    ):
        path, expected = Path(env.get(variable, "")), env.get(hash_variable, "")
        if not path.is_absolute() or not path.is_file() or not re.fullmatch(r"[0-9a-f]{64}", expected):
            errors.append(f"pinned {label} path and digest required")
            continue
        try:
            measured = sha256(path)
            assets[label] = dict(path=str(path), sha256=measured)
            if measured != expected:
                errors.append(f"pinned {label} digest mismatch")
        except OSError:
            errors.append(f"unreadable {label} asset")
    for label in ("base", "layer"):
        if label in assets and env.get(f"FAAS_DATA_REPAIR_RUNNER_{label.upper()}_KEY") != assets[label]["path"]:
            errors.append(f"runner {label} key differs from test image path")
    version = env.get("FAAS_TEST_FC_VERSION", "")
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        errors.append("FAAS_TEST_FC_VERSION required")
    else:
        for tool in ("firecracker", "jailer"):
            if shutil.which(tool):
                try:
                    actual = capture(tool, "--version")
                    if not re.search(rf"\bv{re.escape(version)}\b", actual):
                        errors.append(f"{tool} version mismatch")
                    host[tool] = dict(path=shutil.which(tool), version=actual, sha256=sha256(Path(shutil.which(tool))))
                except (OSError, subprocess.CalledProcessError):
                    errors.append(f"{tool} identity unavailable")
    dump = shutil.which(env.get("GREGALE_DATA_REPAIR_PG_DUMP", "pg_dump"))
    if not dump:
        errors.append("PostgreSQL 16 pg_dump required")
    else:
        try:
            if not re.search(r"\b16(?:\.|\b)", capture(dump, "--version")):
                errors.append("pg_dump must be PostgreSQL 16")
        except subprocess.CalledProcessError:
            errors.append("pg_dump version unavailable")
    return dict(errors=errors, host=host, assets=assets)


def verify_events(path):
    running, passed, phase_runs, phases = set(), set(), set(), set()
    package_started = package_passed = False
    for line in path.read_text().splitlines():
        if not line.startswith("{"):
            continue  # make/build command output is retained but proves no test.
        event = json.loads(line)
        action, package, test = event.get("Action"), event.get("Package"), event.get("Test", "")
        if action in ("fail", "skip", "build-fail"):
            raise ValueError("failed or skipped native test")
        if package != PACKAGE:
            continue
        if package_passed:
            raise ValueError("events after package pass")
        if not test:
            if action == "start" and not package_started:
                package_started = True
            elif action == "pass" and package_started and passed == set(TESTS):
                package_passed = True
            elif action != "output":
                raise ValueError("invalid package event sequence")
        elif test in TESTS:
            if not package_started:
                raise ValueError("test before package start")
            if action == "run":
                if test in running or test in passed:
                    raise ValueError("repeated native test")
                running.add(test)
            elif action == "pass":
                if test not in running or test in passed:
                    raise ValueError("native pass without run")
                passed.add(test)
            elif action == "output" and test == CRASH and test in running and test not in passed:
                # The crash suite reexecs under unshare. Its subtest results are
                # output of the outer test, not outer test2json subtest events.
                for output in event.get("Output", "").splitlines():
                    started = re.match(rf"\s*=== RUN\s+{CRASH}/([a-z_]+)(?:\s|$)", output)
                    if started:
                        phase = started.group(1)
                        if phase not in PHASES or phase in phase_runs:
                            raise ValueError("invalid crash phase start")
                        phase_runs.add(phase)
                    match = re.match(rf"\s*--- (PASS|FAIL|SKIP): {CRASH}/([a-z_]+)(?:\s|$)", output)
                    if match:
                        verdict, phase = match.groups()
                        if verdict != "PASS" or phase not in phase_runs or phase in phases:
                            raise ValueError("invalid crash phase result")
                        phases.add(phase)
    if not package_passed or passed != set(TESTS) or phases != set(PHASES):
        raise ValueError("required native tests or crash phases missing")
    return dict(tests=sorted(passed), crash_phases=sorted(phases))


def run_logged(command, path, env):
    dsn = env.get("DATABASE_URL", "")
    secrets = {dsn, urlsplit(dsn).password or "", unquote(urlsplit(dsn).password or "")} - {""}
    with path.open("x", encoding="utf-8") as output:
        path.chmod(0o600)
        process = subprocess.Popen(command, cwd=ROOT, env=env, text=True,
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            for line in process.stdout:
                for secret in sorted(secrets, key=len, reverse=True):
                    line = line.replace(secret, "[redacted]")
                output.write(line)
                output.flush()
                print(line, end="", flush=True)
            return process.wait()
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait(timeout=15)
            process.stdout.close()


def qualify(directory, env, preflight_only=False):
    report = dict(version=1, status="blocked", started_at=now(), qualification_scope="native_manager_and_backends",
                  qualified_native_manager=False, serving_daemon_and_node_failure_qualified=False)
    lock = None
    try:
        source = source_identity()
        write_json(directory / "source.json", source)
        report["source"] = {key: value for key, value in source.items() if key != "files"}
        preflight = inspect_host(env)
        write_json(directory / "preflight.json", preflight)
        report["preflight_errors"] = preflight["errors"]
        if preflight["errors"]:
            for reason in preflight["errors"]:
                print("native repair preflight: " + reason, file=sys.stderr)
            return 1
        if preflight_only:
            report["status"] = "preflight_passed"
            return 0
        lock = open("/var/lock/faas-builder-acceptance.lock", "a", encoding="utf-8")
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        report["status"] = "failed"
        leak = ["bash", str(ROOT / "deploy/scripts/leakcheck.sh")]
        report["leakcheck_before_exit"] = run_logged(leak, directory / "leakcheck-before.log", env)
        if report["leakcheck_before_exit"]:
            return 1
        test_env = dict(env, FAAS_DATA_REPAIR_NATIVE_TEST="1")
        for name in ("GREGALE_REPAIR_CRASH_CONFIG", "GREGALE_REPAIR_CRASH_ISOLATED"):
            test_env.pop(name, None)
        command = ["make", "test-metal", "GO=" + preflight["host"]["go"], "PKGS=./pkg/fcvm",
                   "RUN_REGEX=^(" + "|".join(TESTS) + ")$", "RUN_ARGS=-json -v -timeout=20m"]
        try:
            report["native_exit"] = run_logged(command, directory / "native.log", test_env)
        finally:
            report["leakcheck_after_exit"] = run_logged(leak, directory / "leakcheck-after.log", env)
        report["verified_execution"] = verify_events(directory / "native.log")
        after = source_identity()
        report["source_unchanged"] = after == source
        assets = [*preflight["assets"].values(), *(preflight["host"][tool] for tool in ("firecracker", "jailer"))]
        report["assets_unchanged"] = all(sha256(Path(asset["path"])) == asset["sha256"] for asset in assets)
        if report["native_exit"] or report["leakcheck_after_exit"] or not report["source_unchanged"] or not report["assets_unchanged"]:
            return 1
        report["status"] = "passed"
        report["qualified_native_manager"] = True
        return 0
    except (OSError, ValueError, subprocess.SubprocessError, KeyboardInterrupt) as error:
        # Errors may contain a DSN. Persist the class; command logs contain only
        # disposable-fixture diagnostics and no environment dump.
        report["error_type"] = type(error).__name__
        return 1
    finally:
        if lock:
            lock.close()
        report["completed_at"] = now()
        report["evidence_sha256"] = {path.name: sha256(path) for path in directory.iterdir() if path.is_file()}
        write_json(directory / "result.json", report)
        print(f"native repair qualification: {report['status']}; evidence: {directory}", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--preflight-only", action="store_true")
    parser.add_argument("--evidence-dir", type=Path, default=os.environ.get("GREGALE_REPAIR_NATIVE_EVIDENCE_DIR"))
    args = parser.parse_args()
    directory = Path(args.evidence_dir).resolve() if args.evidence_dir else Path(tempfile.mkdtemp(prefix="gregale-repair-native-"))
    if directory == ROOT or ROOT in directory.parents:
        parser.error("evidence must be outside the source checkout")
    directory.mkdir(parents=True, exist_ok=True)
    if any(directory.iterdir()):
        parser.error("evidence directory must be empty; existing runs are never overwritten")
    directory.chmod(0o700)
    return qualify(directory, dict(os.environ), args.preflight_only)


if __name__ == "__main__":
    sys.exit(main())
