#!/usr/bin/env python3
"""Read-only native host witnesses for ADR-1547's isolated migration fixture."""

import hashlib
import json
import os
import pathlib
import platform
import stat
import subprocess
import sys


ASSETS = ("vmmd", "schedd", "guest_init", "node22", "node24", "kernel",
          "builder_base", "firecracker", "leakcheck", "leakcheck_native_loops")
MARKER = pathlib.Path("/etc/faas/function-migration-acceptance-host")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(path):
    result = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            result.update(block)
    return result.hexdigest()


def start(pid):
    raw = pathlib.Path(f"/proc/{pid}/stat").read_text()
    return raw[raw.rfind(")") + 2:].split()[19]


def service_pid(unit):
    result = subprocess.run(["systemctl", "show", "--property=MainPID", "--value", unit],
                            capture_output=True, text=True, check=True)
    pid = int(result.stdout.strip())
    require(pid > 0, "native fixture service is not running")
    return pid


def proof(request, full):
    require(os.geteuid() == 0 and platform.system() == "Linux" and platform.machine() == "x86_64",
            "requires root on native x86_64 Linux")
    require(stat.S_ISCHR(os.stat("/dev/kvm").st_mode), "requires /dev/kvm")
    require(subprocess.run(["systemd-detect-virt", "--quiet"], capture_output=True).returncode == 1,
            "virtualized or unclassified compute hosts cannot qualify")
    marker_stat = MARKER.lstat()
    require(stat.S_ISREG(marker_stat.st_mode) and marker_stat.st_uid == 0 and not marker_stat.st_mode & 0o022,
            "native fixture marker must be a root-owned, non-writable regular file")
    marker = json.loads(MARKER.read_text())
    for name in ("fixture_id", "revision", "catalog"):
        require(marker.get(name) == request[name], "native fixture marker scope differs")
    host = request["host"]
    require(marker.get("node_id") == host["node_id"], "native fixture node differs")
    require(set(host["assets"]) == set(ASSETS) and set(request["builds"]) == set(ASSETS),
            "native fixture assets are incomplete")
    for path in host["assets"].values():
        require(isinstance(path, str) and path.startswith("/") and str(pathlib.PurePosixPath(path)) == path
                and ".." not in pathlib.PurePosixPath(path).parts and not any(c in path for c in "\x00\r\n\t :"),
                "native fixture asset path is invalid")
    require(pathlib.Path(host["assets"]["leakcheck_native_loops"]) ==
            pathlib.Path(host["assets"]["leakcheck"]).with_name("leakcheck_native_loops.py"),
            "native loop checker must be the leakcheck script's sibling")
    vmmd, schedd = service_pid("faas-vmmd.service"), service_pid("faas-schedd.service")
    observed = {
        "version": 1, "fixture_id": request["fixture_id"], "revision": request["revision"],
        "catalog": request["catalog"], "node_id": host["node_id"], "root": True,
        "native_x86": True, "kvm": True, "machine_sha256": digest("/etc/machine-id"),
        "boot_sha256": digest("/proc/sys/kernel/random/boot_id"), "vmmd_pid": vmmd,
        "schedd_pid": schedd, "vmmd_start": start(vmmd), "schedd_start": start(schedd),
        "running_vmmd_sha256": digest(f"/proc/{vmmd}/exe"),
        "running_schedd_sha256": digest(f"/proc/{schedd}/exe"),
    }
    require(observed["running_vmmd_sha256"] == request["builds"]["vmmd"] and
            observed["running_schedd_sha256"] == request["builds"]["schedd"],
            "running native daemons differ from the fresh source build")
    if full:
        observed["assets"] = {name: digest(path) for name, path in host["assets"].items()}
        require(observed["assets"] == request["builds"], "staged native assets differ from the fresh source build")
    if "proof" in host:
        baseline = host["proof"]
        for key in ("machine_sha256", "boot_sha256", "vmmd_pid", "schedd_pid", "vmmd_start", "schedd_start"):
            require(observed[key] == baseline[key], "native host or daemon changed during successful migration")
    return observed


def owner(request, retired):
    proof(request, False)
    expected = request["owner"]
    pid, uid, generation = expected["pid"], expected["uid"], expected["start"]
    netns = expected["netns"]
    require(isinstance(pid, int) and 0 < pid <= 2147483647 and isinstance(uid, int) and uid > 0
            and isinstance(generation, str) and generation.isdecimal() and int(generation) > 0,
            "native process identity is invalid")
    require(isinstance(netns, str) and netns not in ("", ".", "..")
            and all(c.isascii() and (c.isalnum() or c in "_.-") for c in netns), "native netns is invalid")
    directory = pathlib.Path(f"/proc/{pid}")
    named = pathlib.Path("/run/netns") / netns
    try:
        alive = start(pid) == generation
    except FileNotFoundError:
        alive = False
    if retired:
        require(not alive and not named.exists(), "original native process or network namespace remains")
        return {"retired": True}
    require(alive, "native receipt does not identify the live jailer child")
    uid_line = next(line for line in (directory / "status").read_text().splitlines() if line.startswith("Uid:"))
    require([int(value) for value in uid_line.split()[1:]] == [uid] * 4,
            "native process does not run under its receipted jail UID")
    require(digest(directory / "exe") == request["builds"]["firecracker"], "native process is not the pinned Firecracker")
    require((directory / "ns/net").stat().st_ino == named.stat().st_ino,
            "native process is outside the receipted network namespace")
    return {"verified": True}


def main(request):
    action = request["action"]
    if action == "preflight":
        return proof(request, True)
    if action in ("owner", "retired"):
        return owner(request, action == "retired")
    require(action == "leakcheck", "unsupported native witness action")
    proof(request, True)
    completed = subprocess.run(["bash", request["host"]["assets"]["leakcheck"]], capture_output=True)
    require(completed.returncode == 0, "native leakcheck failed")
    return {"leakcheck": True, "log_sha256": hashlib.sha256(completed.stdout + completed.stderr).hexdigest()}


if __name__ == "__main__":
    try:
        data = sys.stdin.buffer.read(65537)
        require(len(data) <= 65536, "native witness request is too large")
        print(json.dumps(main(json.loads(data))))
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError):
        print(json.dumps({"error": "native host prerequisite or witness failed"}))
        sys.exit(1)
