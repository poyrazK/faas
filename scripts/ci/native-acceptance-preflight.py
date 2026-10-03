#!/usr/bin/env python3
"""Read-only host checks; the only writes are the requested evidence report."""

import argparse
import ctypes as ct
import ctypes.util
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import stat
import subprocess
import sys


POSTGRES_SQL = ("SELECT has_database_privilege(current_user,current_database(),'CREATE') AND "
                "has_schema_privilege(current_user,'public','USAGE') AND "
                "(EXISTS(SELECT 1 FROM pg_extension WHERE extname='citext' AND "
                "extnamespace='public'::regnamespace) OR "
                "(has_schema_privilege(current_user,'public','CREATE') AND EXISTS(SELECT 1 FROM "
                "pg_available_extension_versions WHERE name='citext' AND trusted)));")


def postgres_probe(dsn):
    """Use the installed libpq without exposing a URI/password in process argv.

    PQconnectdbParams expands dbname first; the following explicit parameters
    override connection-string options, enforcing timeouts and read-only SQL.
    The caller runs this in a bounded subprocess, including library/DNS work.
    """
    lib = ct.CDLL(ctypes.util.find_library("pq") or "libpq.so.5")
    signatures = {
        "PQconnectdbParams": ([ct.POINTER(ct.c_char_p), ct.POINTER(ct.c_char_p), ct.c_int], ct.c_void_p),
        "PQstatus": ([ct.c_void_p], ct.c_int),
        "PQexec": ([ct.c_void_p, ct.c_char_p], ct.c_void_p),
        "PQresultStatus": ([ct.c_void_p], ct.c_int),
        "PQntuples": ([ct.c_void_p], ct.c_int),
        "PQnfields": ([ct.c_void_p], ct.c_int),
        "PQgetvalue": ([ct.c_void_p, ct.c_int, ct.c_int], ct.c_char_p),
        "PQclear": ([ct.c_void_p], None),
        "PQfinish": ([ct.c_void_p], None),
    }
    for name, (arguments, result) in signatures.items():
        function = getattr(lib, name)
        function.argtypes, function.restype = arguments, result
    keywords = (ct.c_char_p * 4)(b"dbname", b"connect_timeout", b"options", None)
    values = (ct.c_char_p * 4)(dsn.encode(), b"5",
        b"-c default_transaction_read_only=on -c statement_timeout=5000", None)
    connection = lib.PQconnectdbParams(keywords, values, 1)
    result = None
    try:
        if not connection or lib.PQstatus(connection) != 0:  # CONNECTION_OK
            return False
        result = lib.PQexec(connection, POSTGRES_SQL.encode())
        return bool(result and lib.PQresultStatus(result) == 2 and  # PGRES_TUPLES_OK
                    lib.PQntuples(result) == 1 and lib.PQnfields(result) == 1 and
                    lib.PQgetvalue(result, 0, 0) == b"t")
    finally:
        if result:
            lib.PQclear(result)
        if connection:
            lib.PQfinish(connection)


class Host:
    def path(self, name):
        return Path(name)

    def run(self, argv, env=None):
        try:
            result = subprocess.run(argv, capture_output=True, text=True, timeout=15, env=env)
            return result.returncode, result.stdout.strip()
        except (OSError, subprocess.TimeoutExpired):
            # Never print command output/errors: database and registry credentials
            # can appear in them. The failed check supplies a safe repair hint.
            return 1, ""

    def tool(self, name):
        return shutil.which(name) is not None

    def identity(self):
        return os.geteuid(), platform.system(), platform.machine(), os.cpu_count()

    def available_bytes(self, path):
        space = os.statvfs(path)
        return space.f_bavail * space.f_frsize


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return "sha256:" + digest.hexdigest()


def builder_drive_budget(source):
    """Read the pinned builderd owner rather than copying its capacity policy."""
    constants = dict(re.findall(r"^const (BuildDrive\w+) = (.+)$", source, re.M))
    names = ("BuildDriveMinWorkingSetBytes", "BuildDriveHostReserveBytes")
    if constants.get("BuildDriveMinFreeBytes") != " + ".join(names):
        raise ValueError("unknown builder drive capacity expression")
    budget = 0
    for name in names:
        value = re.fullmatch(r"(\d+) << (\d+)", constants.get(name, ""))
        if value is None or not 0 <= int(value[2]) <= 40:
            raise ValueError("unknown builder drive capacity constant")
        budget += int(value[1]) << int(value[2])
    return budget


def inspect_base(path, scan_path, digest_path):
    """Mirror VMMD's fix-available CRITICAL admission, including legacy fallback."""
    digest = sha256(path)
    scan = json.loads(scan_path.read_text())
    findings = scan.get("findings")
    gate = scan.get("fix_available_findings")
    if gate is None:
        gate = findings
    for counts in (findings, gate):
        if not isinstance(counts, dict) or "CRITICAL" not in counts:
            raise ValueError("scan has no CRITICAL count")
        if any(type(value) is not int or value < 0 for value in counts.values()):
            raise ValueError("scan findings are invalid")
    if gate["CRITICAL"] != 0:
        raise ValueError("scan blocks VM admission")
    # Production staging records the exact filesystem scanned. A legacy scan
    # cannot prove that a newly copied canonical artifact was scanned.
    if not scan.get("source") or Path(scan["source"]).resolve() != path.resolve():
        raise ValueError("scan source does not match the canonical artifact")
    source_ref = next((line.removeprefix("source-ref=") for line in
                       digest_path.read_text().splitlines() if line.startswith("source-ref=")), "")
    if not source_ref or scan.get("image") != source_ref:
        raise ValueError("scan and digest sidecar refer to different images")
    return {"sha256": digest, "source_ref": source_ref,
            "scan_sha256": sha256(scan_path), "scanned_at": scan.get("scanned_at")}


def collect(args, host=None):
    host = host or Host()
    checks, artifacts = [], {}

    def check(name, ok, repair, detail=None):
        checks.append({"name": name, "status": "passed" if ok else "failed",
                       "repair": None if ok else repair, "detail": detail})

    uid, system, arch, cpus = host.identity()
    check("linux_amd64_root", uid == 0 and system == "Linux" and arch == "x86_64",
          "Run as root on the designated Linux/amd64 KVM acceptance host.")
    kvm = host.path("/dev/kvm")
    check("kvm", kvm.exists() and stat.S_ISCHR(kvm.stat().st_mode) and os.access(kvm, os.R_OK | os.W_OK),
          "Provision a KVM host with an accessible /dev/kvm.")
    check("designated_host", host.path("/etc/faas/builder-acceptance-host").is_file(),
          "ansible-playbook -i INVENTORY deploy/ansible/native-acceptance-host.yml")
    tools = "firecracker jailer gcc ip iptables nft tc flock make systemctl systemd-run readlink".split()
    tools += "busybox file unshare mkfs.ext4 e2fsck install truncate".split() if args.mode == "metal" else \
        "debugfs mkfs.ext4 e2fsck psql tar truncate".split()
    missing = [tool for tool in tools if not host.tool(tool)]
    check("host_tools", not missing,
          "ansible-playbook -i INVENTORY deploy/ansible/native-acceptance-host.yml", missing)
    rc, version = host.run([args.go, "version"], dict(os.environ, GOTOOLCHAIN="local"))
    go_mod = host.path(str(Path(args.repo_root) / "go.mod"))
    wanted = re.search(r"^go (\S+)", go_mod.read_text(), re.M) if go_mod.is_file() else None
    check("pinned_go", rc == 0 and wanted is not None and f"go{wanted[1]} " in version,
          "Use the Go toolchain pinned in go.mod.")
    marker = host.path(str(Path(args.repo_root) / f".faas-{args.mode if args.mode == 'metal' else 'e2e'}-source-sha"))
    check("source_archive", bool(re.fullmatch(r"[0-9a-f]{40}", args.source_sha)) and
          marker.is_file() and marker.read_text().strip() == args.source_sha,
          "Stage an exact git archive and its matching .faas-*-source-sha marker.")
    rc, fc = host.run(["firecracker", "--version"])
    check("firecracker_version", rc == 0 and bool(re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", args.fc_version)) and
          re.search(r"\bv" + re.escape(args.fc_version) + r"\b", fc) is not None,
          "Provision the Firecracker version selected by FAAS_TEST_FC_VERSION.")
    controllers = host.path("/sys/fs/cgroup/cgroup.controllers")
    enabled = host.path("/sys/fs/cgroup/cgroup.subtree_control")
    check("cgroup_v2_delegation", controllers.is_file() and enabled.is_file() and
          {"cpu", "memory"} <= set(controllers.read_text().split()) and
          {"cpu", "memory"} <= set(enabled.read_text().split()),
          "ansible-playbook -i INVENTORY deploy/ansible/native-acceptance-host.yml")
    rc, _ = host.run(["ip", "link", "show", "br-tenants"])
    check("tenant_bridge", rc == 0, "Provision br-tenants with the native acceptance playbook.")
    forwarding = host.path("/proc/sys/net/ipv4/ip_forward")
    check("ipv4_forwarding", forwarding.is_file() and forwarding.read_text().strip() == "1",
          "Provision IPv4 forwarding with the native acceptance playbook.")
    kernel = host.path(args.kernel)
    try:
        artifacts["test_kernel"] = {"path": args.kernel, "sha256": sha256(kernel)}
        check("test_kernel", True, "")
    except OSError:
        check("test_kernel", False, "Stage the pinned kernel or set FAAS_TEST_KERNEL to it.")
    backend = (os.environ.get("FAAS_STORAGE_BACKEND") or "local").strip().lower()
    root = Path(os.environ.get("FAAS_STORAGE_ROOT") or "/srv/fc")
    if args.mode == "e2e":
        check("storage_backend", backend in {"local", "oci", "gcs"},
              "Use FAAS_STORAGE_BACKEND=local, oci, or gcs as supported by pkg/storage.")
        # The persistent HTTP bridge binds here via ip netns exec; neither the
        # bridge nor VMMD creates the parent. Missing tmpfiles provisioning
        # otherwise turns successful restores into opaque forwarding 503s.
        stream_root = host.path("/var/run/faas/stream")
        check("stream_bridge_directory", stream_root.is_dir(),
              "Provision /run/faas/stream with compute_only_service's faas.conf "
              "tmpfiles contract (root:faas, mode 0770).",
              {"path": "/var/run/faas/stream"})
        # The source-build harness creates drive1 under Go's temporary root,
        # even when artifacts use a remote backend. Mirror builderd's guard.
        drive_parent = os.environ.get("TMPDIR") or "/tmp"
        try:
            required = builder_drive_budget(host.path(str(Path(args.repo_root) /
                "pkg/builderd/drive.go")).read_text())
            available = host.available_bytes(host.path(drive_parent))
            check("builder_drive_capacity", available >= required,
                  "Provision free space under " + drive_parent +
                  " for pkg/builderd.BuildDriveMinFreeBytes; retire only completed run staging.",
                  {"path": drive_parent, "available_bytes": available, "required_bytes": required})
        except (OSError, ValueError):
            check("builder_drive_capacity", False,
                  "Verify the temporary filesystem and align preflight with pkg/builderd.BuildDriveMinFreeBytes.")
        if backend == "local":
            # VMMD inherits this directory's shared GC group when capturing
            # snapshots; it cannot create the absent root itself.
            snapshot_root = host.path(str(root / "snap"))
            check("snapshot_directory", snapshot_root.is_dir(),
                  "Provision " + str(root / "snap") + " using compute_only_service's "
                  "create imaged runtime dirs task (root:faas, mode 2770).")
            # kernel_path in the harness TOML is deprecated; VMMD uses this
            # storage key. Checking only FAAS_TEST_KERNEL concealed a missing key.
            key = "kernel/" + args.fc_version
            path = host.path(str(root / key))
            try:
                digest = sha256(path)
                artifacts[key] = {"sha256": digest}
                check("canonical_kernel", digest == artifacts.get("test_kernel", {}).get("sha256"),
                      "sudo install -D -m 0644 " + shlex.quote(args.kernel) + " " + shlex.quote(str(root / key)))
            except OSError:
                check("canonical_kernel", False, "sudo install -D -m 0644 " +
                      shlex.quote(args.kernel) + " " + shlex.quote(str(root / key)))
            # Additional runtime keys are explicit: fixture-only lanes and
            # self-contained OCI images must not require unused runtime bases.
            for key in ["base/runner-builder-amd64.ext4", *args.runtime_base_key]:
                if not re.fullmatch(r"base/[A-Za-z0-9_.-]+\.ext4", key):
                    check("base_key", False, "Use a canonical base/*.ext4 storage key.")
                    continue
                try:
                    artifacts[key] = inspect_base(host.path(str(root / key)),
                        host.path(str(root / ("scans/" + key.removeprefix("base/") + ".scan.json"))),
                        host.path(str(root / (key + ".digest"))))
                    check(key, True, "")
                except (OSError, ValueError, TypeError, AttributeError):
                    check(key, False, "Stage " + key + " with faas-imaged EnsureBaseExt4; retain its digest and canonical Grype scan sidecars.")
        else:
            checks.append({"name": "remote_storage_artifacts", "status": "unverified",
                           "detail": "Remote storage resolution and scan admission remain required in E2E; no local-file requirement is inferred."})
        env = dict(os.environ, FAAS_E2E_DATABASE_URL=os.environ.get("FAAS_E2E_DATABASE_URL",
                    os.environ.get("DATABASE_URL", "postgres:///faas_e2e?host=/run/postgresql&user=faas")))
        rc, privileges = host.run([sys.executable, str(Path(__file__).resolve()), "postgres-probe"], env)
        check("postgres_privileges", rc == 0 and privileges == "t",
              "Provision the isolated faas_e2e database and faas role with the native acceptance playbook; verify /etc/faas/e2e-acceptance.env.")
    if args.mode == "metal":
        rc, binary = host.run(["file", shutil.which("busybox") or "busybox"])
        check("static_busybox", rc == 0 and "statically linked" in binary,
              "Install busybox-static for the disposable metal guest fixture.")
    rc, virtualization = host.run(["systemd-detect-virt", "--vm"])
    virtualization = virtualization if virtualization and (rc == 0 or virtualization == "none") else "unknown"
    meminfo = host.path("/proc/meminfo")
    memory = re.search(r"^MemTotal:\s+(\d+) kB", meminfo.read_text(), re.M) if meminfo.is_file() else None
    return {"schema_version": 1, "source_sha": args.source_sha, "mode": args.mode, "lane": args.lane,
            "created_at": dt.datetime.now(dt.timezone.utc).isoformat(), "go_version": version,
            "firecracker_version": args.fc_version, "host": {"system": system, "arch": arch,
            "cpu_count": cpus, "memory_kib": int(memory[1]) if memory else None,
            "virtualization": virtualization, "native_host": virtualization == "none",
            "performance_qualified": False}, "storage_backend": backend, "artifacts": artifacts,
            "checks": checks, "preflight": "blocked" if any(c["status"] == "failed" for c in checks) else "ready",
            "acceptance": {"status": "not_run", "cleanup": "not_run"}}


def finish(report, logs, exit_code, cleanup):
    results, missing = [], []
    for name in logs:
        path = Path(name)
        if not path.is_file():
            missing.append(name)
            continue
        for line in path.read_text(errors="replace").splitlines():
            match = re.match(r"^(\s*)--- (PASS|SKIP|FAIL): (\S+)", line)
            if match:
                results.append({"log": name, "test": match[3], "result": match[2], "subtest": bool(match[1])})
    counts = {status.lower(): sum(r["result"] == status and not r["subtest"] for r in results)
              for status in ("PASS", "SKIP", "FAIL")}
    status = "failed" if exit_code or cleanup != "passed" or any(r["result"] == "FAIL" for r in results) else "passed"
    if status == "passed" and (missing or not logs or not counts["pass"] or
            any(r["result"] == "SKIP" for r in results) or
            any(c["status"] != "passed" for c in report["checks"])):
        status = "incomplete"
    report["acceptance"] = {"status": status, "process_exit_code": exit_code, "cleanup": cleanup,
                            "counts": counts, "tests": results, "missing_logs": missing}
    return report


def main():
    # Private child mode: errors may contain credentials, so output only a
    # boolean. Host.run also suppresses stderr and enforces a 15-second bound.
    if sys.argv[1:] == ["postgres-probe"]:
        try:
            ready = postgres_probe(os.environ["FAAS_E2E_DATABASE_URL"])
        except (OSError, ValueError, KeyError):
            ready = False
        if ready:
            print("t")
        return 0 if ready else 1
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    check = sub.add_parser("check")
    for name in ("repo-root", "source-sha", "go", "kernel", "fc-version", "report"):
        check.add_argument("--" + name, required=True)
    check.add_argument("--mode", choices=("metal", "e2e"), required=True)
    check.add_argument("--lane", default="all")
    check.add_argument("--runtime-base-key", action="append", default=[])
    final = sub.add_parser("finish")
    final.add_argument("--report", required=True)
    final.add_argument("--log", action="append", default=[])
    final.add_argument("--exit-code", type=int, required=True)
    final.add_argument("--cleanup", choices=("passed", "failed"), required=True)
    args = parser.parse_args()
    if args.command == "check":
        report = collect(args)
    else:
        report = finish(json.loads(Path(args.report).read_text()), args.log, args.exit_code, args.cleanup)
    Path(args.report).write_text(json.dumps(report, indent=2) + "\n")
    if args.command == "check":
        print(f"acceptance preflight: {report['preflight']}; evidence: {args.report}")
        for item in report["checks"]:
            if item["status"] == "failed":
                print(f"acceptance preflight: {item['name']}: {item['repair']}", file=sys.stderr)
        return 1 if report["preflight"] == "blocked" else 0
    print(f"acceptance evidence: {report['acceptance']['status']}; report: {args.report}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
