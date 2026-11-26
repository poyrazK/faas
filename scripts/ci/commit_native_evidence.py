#!/usr/bin/env python3
"""Bind Commit's native gate to source, host fixtures and sanitized evidence."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile


PACKAGE = "github.com/onebox-faas/faas/cmd/e2e"
REQUIRED = (
    "TestSourceDeployWakeMetal",
    "TestSourceDeployWakeMetal/commit-snapshot-wake",
    "TestSourceDeployWakeMetal/commit-cold-boot-wake",
    "TestSourceDeployWakeMetal/commit-consumer-crash-recovery",
)
BINARIES = (
    "apid", "schedd", "vmmd", "imaged", "gatewayd-internal", "gatewayd-public",
    "meterd", "builderd", "vmmd-stream-bridge", "vmmd-raw-bridge",
    "vmmd-tcp-bridge", "vmmd-udp-bridge", "vmmd-jail-helper", "vmmd-guestdisk",
)
IDENTITIES = ("kernel", "builder_base", "guest_init", "firecracker", "jailer", "go_tool")
SHA256 = re.compile(r"[0-9a-f]{64}\Z")
COMMIT = re.compile(r"[0-9a-f]{40}\Z")
REASONS = {
    "setup_failed", "interrupted", "completed", "cleanup_failed", "unsupported_host", "missing_tool",
    "invalid_host_configuration", "invalid_firecracker_version", "postgres_skipped", "missing_fixture",
    "missing_postgres_binaries", "invalid_postgres_user", "missing_snapshot_mountpoint", "missing_tenant_bridge",
    "forwarding_disabled", "missing_public_interface", "systemd_unavailable", "unsupported_container",
    "unknown_host_class", "busy_host", "active_service", "active_firecracker", "insufficient_disk",
    "source_mismatch", "ignored_source_files", "go_version_mismatch", "firecracker_version_mismatch",
    "initial_leakcheck_failed", "native_gate_failed", "test_processes_remain",
}
CONFIG_REQUIRED = {
    "FAAS_TEST_KERNEL", "FAAS_TEST_FC_VERSION", "FAAS_BUILDER_BASE_PATH",
    "FAAS_COMMIT_TLS_PG_BIN_DIR", "FAAS_COMMIT_TLS_PG_USER",
}
CONFIG_OPTIONAL = {"FAAS_PUBLIC_IFACE"}


def parse_config(text):
    values = {}
    for line in text.splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        match = re.fullmatch(r"([A-Z][A-Z0-9_]*)=([^\s]+)", line)
        if not match:
            raise ValueError("invalid configuration line")
        key, value = match.groups()
        if (key not in CONFIG_REQUIRED | CONFIG_OPTIONAL or key in values
                or any(char in value for char in "'\"\\$`;")):
            raise ValueError("invalid configuration field")
        values[key] = value
    if not CONFIG_REQUIRED <= set(values):
        raise ValueError("missing configuration field")
    return values


def digest_file(path):
    path = Path(path)
    if not path.is_file() or path.stat().st_size == 0:
        raise ValueError("missing identity")
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def fingerprint(repo, expected, binaries, paths):
    if not COMMIT.fullmatch(expected):
        raise ValueError("invalid commit")
    def git(*args):
        return subprocess.check_output(["git", "-C", str(repo), *args])
    revision = git("rev-parse", "HEAD").decode().strip()
    if revision != expected or git("status", "--porcelain", "--untracked-files=all"):
        raise ValueError("source mismatch")
    if git("ls-files", "--others", "--ignored", "--exclude-standard"):
        raise ValueError("ignored source files are present")
    digest = hashlib.sha256()
    with tempfile.TemporaryFile() as archive:
        subprocess.run(["git", "-C", str(repo), "archive", "--format=tar", "HEAD"], stdout=archive, check=True)
        archive.seek(0)
        for chunk in iter(lambda: archive.read(1024 * 1024), b""):
            digest.update(chunk)
    if set(paths) != set(IDENTITIES):
        raise ValueError("identity set mismatch")
    hashes = {name: digest_file(paths[name]) for name in IDENTITIES}
    hashes["binaries"] = {name: digest_file(Path(binaries) / name) for name in BINARIES}
    return {"source_revision": revision, "source_archive_sha256": digest.hexdigest(), "identities": hashes}


def read_object(path, limit=256 * 1024):
    fd = os.open(path, os.O_RDONLY | os.O_NONBLOCK | getattr(os, "O_NOFOLLOW", 0))
    with os.fdopen(fd, "rb") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
            raise ValueError("invalid evidence")
        data = stream.read(limit + 1)
    if len(data) > limit:
        raise ValueError("oversized evidence")
    value = json.loads(data)
    if not isinstance(value, dict):
        raise ValueError("invalid evidence")
    return value


def valid_fingerprint(value, expected):
    if set(value) != {"source_revision", "source_archive_sha256", "identities"}:
        return False
    if value["source_revision"] != expected or not isinstance(value["source_archive_sha256"], str) or not SHA256.fullmatch(value["source_archive_sha256"]):
        return False
    ids = value["identities"]
    if not isinstance(ids, dict) or set(ids) != set(IDENTITIES) | {"binaries"}:
        return False
    if any(not isinstance(ids[name], str) or not SHA256.fullmatch(ids[name]) for name in IDENTITIES):
        return False
    binaries = ids["binaries"]
    return (isinstance(binaries, dict) and set(binaries) == set(BINARIES)
            and all(isinstance(v, str) and SHA256.fullmatch(v) for v in binaries.values()))


def verify_events(path):
    states = {name: [] for name in REQUIRED}
    fd = os.open(path, os.O_RDONLY | os.O_NONBLOCK | getattr(os, "O_NOFOLLOW", 0))
    with os.fdopen(fd, "r", encoding="utf-8") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > 256 * 1024 * 1024:
            raise ValueError("invalid test events")
        for line in stream:
            if len(line) > 1024 * 1024:
                raise ValueError("oversized event")
            event = json.loads(line)
            if not isinstance(event, dict):
                raise ValueError("invalid event")
            action, test = event.get("Action"), event.get("Test")
            if action in ("fail", "skip"):
                raise ValueError("failed or skipped event")
            if event.get("Package") == PACKAGE and test in states and action in ("run", "pass"):
                states[test].append(action)
    if any(state != ["run", "pass"] for state in states.values()):
        raise ValueError("required test missing or repeated")
    return {name: "passed" for name in REQUIRED}


def check_processes(proc_root, binaries):
    """Reject daemons from this run even when they changed process groups."""
    prefix = str(Path(binaries).resolve()) + os.sep
    with os.scandir(proc_root) as entries:
        for entry in entries:
            if not entry.name.isdecimal():
                continue
            try:
                target = os.readlink(Path(proc_root) / entry.name / "exe")
            except (FileNotFoundError, ProcessLookupError):
                continue
            if target.removesuffix(" (deleted)").startswith(prefix):
                raise ValueError("native test daemon remains")


def verify(directory, expected, outcome):
    result = {"schema": "gregale.dev/commit-native-ci/v1", "result": "blocked",
              "expected_commit": expected if COMMIT.fullmatch(expected) else None,
              "native_kvm_passed": False, "production_qualified": False,
              "host_class": None, "checks": {}, "tests": {}, "errors": []}
    errors = result["errors"]
    if result["expected_commit"] is None:
        errors.append("invalid_expected_commit")
    if outcome != "success":
        errors.append("runner_not_successful")
    try:
        state = read_object(directory / "private" / "state.json")
        keys = {"reason", "native_started", "host_class", "leak_before", "leak_after", "postgres_cleanup", "process_cleanup", "binaries_cleanup", "work_cleanup", "gate_exit"}
        if set(state) != keys or state["reason"] not in REASONS or type(state["native_started"]) is not bool:
            raise ValueError("invalid state")
        if state["host_class"] not in ("bare-metal", "nested-kvm", "unknown"):
            raise ValueError("invalid host class")
        if any(state[key] not in ("pass", "fail", "not_run") for key in ("leak_before", "leak_after", "postgres_cleanup", "process_cleanup", "binaries_cleanup", "work_cleanup")):
            raise ValueError("invalid stage")
        if state["gate_exit"] is not None and type(state["gate_exit"]) is not int:
            raise ValueError("invalid gate exit")
        result["host_class"] = state["host_class"] if state["host_class"] != "unknown" else None
        result["reason"] = state["reason"]
        result["checks"] = {key: state[key] for key in ("leak_before", "leak_after", "postgres_cleanup", "process_cleanup", "binaries_cleanup", "work_cleanup")}
        if state["native_started"]:
            result["result"] = "failed"
        if (state["reason"] != "completed" or not state["native_started"] or state["gate_exit"] != 0
                or state["host_class"] == "unknown" or any(v != "pass" for v in result["checks"].values())):
            errors.append("runner_or_cleanup_incomplete")
    except (OSError, ValueError, TypeError, KeyError, json.JSONDecodeError):
        errors.append("runner_evidence_missing_or_invalid")
    try:
        before = read_object(directory / "private" / "identities-before.json")
        after = read_object(directory / "private" / "identities-after.json")
        if not valid_fingerprint(before, expected) or before != after:
            raise ValueError("identity mismatch")
        result["source_archive_sha256"] = before["source_archive_sha256"]
        result["identities"] = before["identities"]
        result["checks"]["source_and_inputs"] = "pass"
    except (OSError, ValueError, TypeError, KeyError, json.JSONDecodeError):
        result["checks"]["source_and_inputs"] = "fail"
        errors.append("source_or_identity_mismatch")
    try:
        event_path = directory / "private" / "test-events.jsonl"
        result["tests"] = verify_events(event_path)
        result["test_events_sha256"] = digest_file(event_path)
    except (OSError, ValueError, TypeError, json.JSONDecodeError):
        errors.append("test_events_missing_or_invalid")
    if not errors:
        result.update(result="passed", native_kvm_passed=True)
    return result


def write_json(path, data):
    path.write_text(json.dumps(data, sort_keys=True, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="mode", required=True)
    fp = sub.add_parser("fingerprint")
    fp.add_argument("--repo", required=True, type=Path)
    fp.add_argument("--expected", required=True)
    fp.add_argument("--binaries", required=True, type=Path)
    fp.add_argument("--output", required=True, type=Path)
    for name in IDENTITIES:
        fp.add_argument("--" + name.replace("_", "-"), required=True, type=Path)
    verdict = sub.add_parser("verdict")
    verdict.add_argument("--directory", required=True, type=Path)
    verdict.add_argument("--expected", required=True)
    verdict.add_argument("--outcome", default="")
    processes = sub.add_parser("process-check")
    processes.add_argument("--binaries", required=True, type=Path)
    config = sub.add_parser("config")
    config.add_argument("--file", required=True, type=Path)
    args = parser.parse_args()
    if args.mode == "config":
        try:
            values = parse_config(args.file.read_text())
        except (OSError, ValueError, UnicodeError):
            print("Commit native: invalid host configuration", file=sys.stderr)
            return 1
        for key, value in values.items():
            print(key + "=" + value)
        return 0
    if args.mode == "process-check":
        try:
            check_processes(Path("/proc"), args.binaries)
        except (OSError, ValueError):
            print("Commit native: process inventory failed or test daemons remain", file=sys.stderr)
            return 1
        return 0
    if args.mode == "fingerprint":
        paths = {name: getattr(args, name) for name in IDENTITIES}
        write_json(args.output, fingerprint(args.repo, args.expected, args.binaries, paths))
        return 0
    directory = args.directory
    if not directory.is_absolute() or directory.is_symlink() or directory.resolve().is_relative_to(Path(__file__).resolve().parents[2]):
        parser.error("evidence directory must be absolute and outside the checkout")
    if not directory.exists():
        directory.mkdir(mode=0o700)
    info = directory.stat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o022:
        parser.error("evidence directory must be owned by the current user and not writable by others")
    summary = verify(directory, args.expected, args.outcome)
    public = directory / "public"
    if public.is_symlink():
        parser.error("public evidence directory cannot be a symlink")
    public.mkdir(mode=0o755, exist_ok=True)
    info = public.stat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o022:
        parser.error("public evidence directory is not protected")
    directory.chmod(0o711)
    public.chmod(0o755)
    for name, content in (
        ("summary.json", json.dumps(summary, sort_keys=True, indent=2) + "\n"),
        ("summary.md", "## Commit native acceptance\n\nResult: **" + summary["result"] + "**\n\nProduction qualification remains separate.\n"),
    ):
        fd = os.open(public / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o644)
        with os.fdopen(fd, "w") as stream:
            stream.write(content)
        (public / name).chmod(0o644)
    print("Commit native acceptance: " + summary["result"])
    return 0 if summary["native_kvm_passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
