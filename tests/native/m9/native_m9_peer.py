#!/usr/bin/env python3
"""Fixed native-M9 peer operations, sent over authenticated SSH by the gate."""
import base64
import contextlib
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import signal
import stat
import subprocess
import sys
import tempfile
import time
import uuid

LEASE = Path("/run/faas/native-m9-acceptance.owner.json")
MARKER = Path("/etc/faas/m9-acceptance-host")
UNIT = "faas-vmmd.service"


def command(args, timeout=30):
    return subprocess.run(args, check=True, text=True, capture_output=True, timeout=timeout).stdout.strip()


def canonical_uuid(value):
    try:
        return isinstance(value, str) and str(uuid.UUID(value)) == value and uuid.UUID(value).int != 0
    except ValueError:
        return False


def validate(request):
    if not canonical_uuid(request.get("run_id")):
        raise ValueError("canonical M9 run UUID required")
    if not re.fullmatch(r"[0-9a-f]{64}", request.get("vmmd_sha256", "")):
        raise ValueError("reviewed vmmd SHA-256 required")
    if request.get("action") not in {"claim", "pause", "resume", "restore", "crash", "leaks", "release"}:
        raise ValueError("unsupported M9 peer operation")
    if os.geteuid() != 0 or platform.system() != "Linux" or platform.machine() != "x86_64":
        raise ValueError("M9 requires root on native x86_64 Linux")
    marker = MARKER.lstat()
    if not stat.S_ISREG(marker.st_mode) or marker.st_uid != 0 or marker.st_mode & 0o022:
        raise ValueError("root-owned, non-writable M9 acceptance marker required")


def identity(expected_sha):
    fields = command(["systemctl", "show", UNIT, "--property=MainPID,InvocationID,ActiveState"])
    fields = dict(line.split("=", 1) for line in fields.splitlines())
    pid = int(fields.get("MainPID", "0"))
    if pid <= 0 or fields.get("ActiveState") != "active" or not re.fullmatch(r"[0-9a-f]{32}", fields.get("InvocationID", "")):
        raise ValueError("vmmd must have an active, identifiable systemd invocation")
    proc = Path("/proc") / str(pid)
    # The final ')' also handles a comm containing spaces or parentheses.
    start = (proc / "stat").read_text().rsplit(")", 1)[1].split()[19]
    with (proc / "exe").open("rb") as binary:
        digest = hashlib.file_digest(binary, "sha256").hexdigest()
    if digest != expected_sha:
        raise ValueError("running vmmd does not match the reviewed artifact")
    return {"pid": pid, "invocation_id": fields["InvocationID"], "start_ticks": start, "vmmd_sha256": digest}


def load_lease(request):
    info = LEASE.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077 or info.st_nlink != 1:
        raise ValueError("unsafe M9 peer lease")
    data = json.loads(LEASE.read_text())
    if data.get("run_id") != request["run_id"] or data.get("vmmd_sha256") != request["vmmd_sha256"]:
        raise ValueError("peer belongs to a different M9 run")
    return data


def save_lease(data):
    # Replace atomically in the same root-owned directory, and fsync the entry.
    fd, name = tempfile.mkstemp(prefix=".native-m9-", dir=LEASE.parent)
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(data, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, LEASE)
        directory = os.open(LEASE.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(name)


def fenced_signal(expected, sig):
    # A pidfd prevents PID reuse between the identity check and signal delivery.
    fd = os.pidfd_open(expected["pid"])
    try:
        if identity(expected["vmmd_sha256"]) != expected:
            raise ValueError("vmmd invocation changed before fault delivery")
        signal.pidfd_send_signal(fd, sig)
    finally:
        os.close(fd)


def native_host():
    if not hasattr(os, "pidfd_open") or not hasattr(signal, "pidfd_send_signal"):
        raise ValueError("native M9 requires Python pidfd signal support")
    if not stat.S_ISCHR(Path("/dev/kvm").stat().st_mode) or not os.access("/dev/kvm", os.R_OK | os.W_OK):
        raise ValueError("accessible KVM device required")
    if not Path("/sys/fs/cgroup/cgroup.controllers").is_file():
        raise ValueError("cgroups v2 required")
    for kind in ("--vm", "--container"):
        result = subprocess.run(["systemd-detect-virt", kind, "--quiet"], capture_output=True, timeout=10)
        if result.returncode != 1:
            raise ValueError("virtualized or unattested peer cannot qualify native M9")
    return {"boot_id": Path("/proc/sys/kernel/random/boot_id").read_text().strip(), "host_kernel": platform.release()}


def claim(request):
    host = native_host()
    current = identity(request["vmmd_sha256"])
    data = {"run_id": request["run_id"], "vmmd_sha256": request["vmmd_sha256"], "initial": current, "paused": None, **host}
    # No expiry-based takeover: a dead controller may have left a stopped daemon.
    # An existing lease always requires inspection by its original operator.
    fd = os.open(LEASE, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w") as stream:
        json.dump(data, stream)
        stream.flush()
        os.fsync(stream.fileno())
    return data


def resume(request, data):
    paused = data.get("paused")
    if paused is not None:
        fenced_signal(paused, signal.SIGCONT)
        data["paused"] = None
        save_lease(data)
    current = restart_reviewed(request, data) if data.get("crash_requested") else identity(request["vmmd_sha256"])
    return {"restored": True, "daemon": current}


def restart_reviewed(request, data):
    """Complete this run's persisted restart intent, including after disconnect."""
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        try:
            command(["systemctl", "start", UNIT], timeout=10)
            current = identity(request["vmmd_sha256"])
            if current["invocation_id"] != data["initial"]["invocation_id"]:
                return current
        except (OSError, ValueError, subprocess.SubprocessError):
            pass
        time.sleep(0.1)
    raise ValueError("no new reviewed vmmd invocation appeared after SIGKILL")


def leakcheck(request):
    files = request.get("leak_files", {})
    names = {"leakcheck.sh", "leakcheck_native_loops.py"}
    if set(files) != names or any(not isinstance(v, str) or len(v) > 65536 for v in files.values()):
        raise ValueError("complete reviewed leak-check bundle required")
    with tempfile.TemporaryDirectory(prefix="faas-m9-leak-") as directory:
        for name, body in files.items():
            p = Path(directory) / name
            p.write_text(body)
            p.chmod(0o600)
        output = command(["bash", str(Path(directory) / "leakcheck.sh")], timeout=45)
    if "leakcheck OK — no leaked" not in output or "LEAK:" in output:
        raise ValueError("peer leak check did not produce its complete success verdict")
    return {"passed": True, "bundle_sha256": {name: hashlib.sha256(body.encode()).hexdigest() for name, body in files.items()}}


def execute(request):
    validate(request)
    action = request["action"]
    if action == "claim":
        return claim(request)
    data = load_lease(request)
    if action in {"resume", "restore"}:
        return resume(request, data)
    if action == "pause":
        if data.get("paused") is not None:
            raise ValueError("source daemon is already paused")
        if identity(request["vmmd_sha256"]) != data["initial"]:
            raise ValueError("source daemon changed before the adoption barrier")
        # Retain the recovery identity before delivering SIGSTOP.
        data["paused"] = data["initial"]
        save_lease(data)
        fenced_signal(data["initial"], signal.SIGSTOP)
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if identity(request["vmmd_sha256"]) != data["initial"]:
                raise ValueError("source invocation changed while observing SIGSTOP")
            state = (Path("/proc") / str(data["initial"]["pid"]) / "stat").read_text().rsplit(")", 1)[1].split()[0]
            if state in {"T", "t"}:
                return {"paused": True, "daemon": data["initial"]}
            time.sleep(0.02)
        raise ValueError("source daemon did not acknowledge SIGSTOP")
    if action == "crash":
        if data.get("paused") is not None:
            raise ValueError("resume the source before the crash drill")
        if data.get("crash_requested"):
            raise ValueError("crash injection was already claimed; use restore to finish its restart")
        data["crash_requested"] = True
        save_lease(data)
        fenced_signal(data["initial"], signal.SIGKILL)
        return {"killed": data["initial"], "restarted": restart_reviewed(request, data)}
    if action == "leaks":
        return leakcheck(request)
    if action == "release":
        if data.get("paused") is not None:
            raise ValueError("refusing to release a paused peer")
        identity(request["vmmd_sha256"])
        result = leakcheck(request)
        LEASE.unlink()
        return {"released": True, **result}
    raise ValueError("unsupported M9 action")


if __name__ == "__main__":
    try:
        raw = base64.b64decode(sys.argv[1], validate=True)
        if len(raw) > 196608:
            raise ValueError("M9 peer request too large")
        result = execute(json.loads(raw))
        print(json.dumps(result, sort_keys=True))
    except Exception as exc:
        # Request bytes and command output can contain private host metadata.
        # Retain the failed lease for inspection; never print the input document.
        print("native M9 peer: " + type(exc).__name__ + ": " + str(exc), file=sys.stderr)
        sys.exit(1)
