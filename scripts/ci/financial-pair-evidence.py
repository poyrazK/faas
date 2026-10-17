#!/usr/bin/env python3
"""Bind physical-pair qualification to both hosts, exact artifacts and cleanup."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shutil
import stat
import sys

ASSETS = ("fcvm.test", "vmmd", "guest-init", "kernel", "base.ext4", "layer.ext4")
PAIR_SOURCE = "pkg/fcvm/financial_pair_metal_test.go"


def read_object(path):
    value = json.loads(Path(path).read_text())
    if not isinstance(value, dict):
        raise ValueError("expected an evidence object")
    return value


def digest(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")
    temporary.replace(path)


def host_report(bundle):
    def identity(path):
        data = Path(path).read_bytes().strip()
        if not data:
            raise ValueError("empty host identity")
        return hashlib.sha256(data).hexdigest()
    source = json.loads((bundle / "source.json").read_text())
    kvm = Path("/dev/kvm")
    return {
        **source, "hostname": platform.node(), "architecture": platform.machine(),
        "kernel": platform.release(), "root": os.geteuid() == 0,
        "kvm": kvm.exists() and stat.S_ISCHR(kvm.stat().st_mode),
        "machine_id_sha256": identity("/etc/machine-id"),
        "boot_id_sha256": identity("/proc/sys/kernel/random/boot_id"),
        "assets": {name: digest(bundle / name) for name in ASSETS},
        "tools": {name: digest(shutil.which(name)) for name in ("firecracker", "jailer")},
    }


def source_tests(root, pair=False):
    paths = [root / PAIR_SOURCE] if pair else sorted((root / "pkg/fcvm").glob("financial_compute*metal_test.go"))
    tests, sources = {}, {}
    pattern = r"TestMetalFinancialPair\w+" if pair else r"TestMetalFinancial\w+"
    for path in paths:
        text = path.read_text()
        sources[str(path.relative_to(root))] = digest(path)
        functions = list(re.finditer(r"^func (\w+)\(", text, re.M))
        for index, match in enumerate(functions):
            name = match[1]
            if not re.fullmatch(pattern, name):
                continue
            body = text[match.end():functions[index + 1].start() if index + 1 < len(functions) else len(text)]
            tests[name] = sorted(set(re.findall(r't\.Run\("([^"\n]+)"', body)))
    if not tests:
        raise ValueError("native financial source selects no tests")
    return tests, sources


def log_contract(path, tests):
    text = Path(path).read_text()
    errors = []
    if not re.search(r"^PASS$", text, re.M):
        errors.append("test executable did not finish successfully")
    if re.search(r"^\s*--- (?:FAIL|SKIP):", text, re.M):
        errors.append("failed or skipped test/subtest")
    for name, phases in tests.items():
        for test in [name] + [name + "/" + phase for phase in phases]:
            if len(re.findall(rf"^\s*--- PASS: {re.escape(test)}(?: |$)", text, re.M)) != 1:
                errors.append(f"{test} must pass exactly once")
    return {"path": str(path), "sha256": digest(path), "required": tests}, errors


def build_report(root, evidence, source_sha, run_id):
    errors, hosts, logs = [], {}, {}
    status = {}
    try:
        status = read_object(evidence / "commands.json")
    except (OSError, ValueError):
        errors.append("command status evidence missing")
    for name in ("single_host", "native", "peer_network", "host_a", "host_b"):
        if type(status.get(name)) is not int or status[name] != 0:
            errors.append(f"{name}: command did not exit zero")
    if not re.fullmatch(r"[a-f0-9]{40}", source_sha) or not re.fullmatch(r"[A-Za-z0-9._-]{1,32}", run_id):
        errors.append("invalid reviewed source or run ID")
    for node in ("a", "b"):
        try:
            host = read_object(evidence / f"host-{node}/ready.json")
            cleanup = read_object(evidence / f"host-{node}/cleanup.json")
            hosts[node] = {"host": host, "cleanup": cleanup}
            if host.get("source_sha") != source_sha or host.get("run_id") != run_id:
                errors.append(f"host {node}: mismatched source/run")
            if not host.get("root") or not host.get("kvm") or host.get("architecture") != "x86_64":
                errors.append(f"host {node}: no native x86_64 root/KVM evidence")
            for field in ("machine_id_sha256", "boot_id_sha256"):
                if not re.fullmatch(r"[a-f0-9]{64}", host.get(field, "")):
                    errors.append(f"host {node}: missing {field}")
            if cleanup.get("source_sha") != source_sha or cleanup.get("run_id") != run_id:
                errors.append(f"host {node}: mismatched cleanup identity")
            for field in ("machine_id_sha256", "boot_id_sha256", "assets", "tools"):
                if cleanup.get(field) != host.get(field):
                    errors.append(f"host {node}: host/boot/artifacts changed during cleanup")
            for check in ("runner_exit", "leakcheck_exit", "post_repair_exit", "restore_exit"):
                if type(cleanup.get(check)) is not int or cleanup[check] != 0:
                    errors.append(f"host {node}: {check} did not pass")
            if not re.fullmatch(r"[a-f0-9]{64}", cleanup.get("leakcheck_sha256", "")):
                errors.append(f"host {node}: cleanup log missing")
            if cleanup.get("leakcheck_sha256") != digest(evidence / f"host-{node}/leakcheck.log"):
                errors.append(f"host {node}: cleanup log changed")
            for kind, names in (("assets", ASSETS), ("tools", ("firecracker", "jailer"))):
                for name in names:
                    if not re.fullmatch(r"[a-f0-9]{64}", host.get(kind, {}).get(name, "")):
                        errors.append(f"host {node}: artifact {name} missing")
        except (OSError, ValueError, KeyError, TypeError):
            errors.append(f"host {node}: required evidence missing or invalid")
    if len(hosts) == 2:
        a, b = hosts["a"]["host"], hosts["b"]["host"]
        for field in ("machine_id_sha256", "boot_id_sha256"):
            if a.get(field) == b.get(field):
                errors.append("two distinct physical hosts/boots are required")
        if a.get("assets") != b.get("assets") or a.get("tools") != b.get("tools"):
            errors.append("native executables, images or host tools differ across hosts")
        try:
            fixture = read_object(evidence / "fixture.json")
            if fixture["assets"] != a.get("assets") or not re.fullmatch(r"[a-f0-9]{64}", fixture["archive_sha256"]):
                errors.append("host assets differ from the controller's compiled fixture")
        except (OSError, ValueError, KeyError, TypeError):
            errors.append("compiled fixture manifest missing or malformed")
    sources = {}
    try:
        component, sources = source_tests(root)
        pair, pair_sources = source_tests(root, pair=True)
        sources.update(pair_sources)
        logs["native"], lane_errors = log_contract(evidence / "native.log", {**component, **pair})
        errors.extend("native: " + error for error in lane_errors)
        network = {name: [] for name in component if re.fullmatch(r"TestMetalFinancialEgress(?:Packet|Offload)Credits", name)}
        if len(network) != 2:
            errors.append("both packet and offload source cases are required")
        logs["peer_network"], lane_errors = log_contract(evidence / "peer-network.log", network)
        errors.extend("peer network: " + error for error in lane_errors)
        text = (evidence / "native.log").read_text()
        owner_hosts = {0: set(), 1: set()}
        for raw in re.findall(r"financial-pair-owner:(\{[^\n]+\})", text):
            owner = json.loads(raw)
            node, host = owner["node"], owner["host"]
            if node not in owner_hosts:
                errors.append("owner evidence names an unexpected node")
                continue
            owner_hosts[node].add(host.get("machine_id_sha256"))
            expected = hosts.get("a" if node == 0 else "b", {}).get("host", {})
            if host.get("machine_id_sha256") != expected.get("machine_id_sha256") or host.get("boot_id_sha256") != expected.get("boot_id_sha256"):
                errors.append("native owner did not execute on the attested host/boot")
            if not host.get("root") or not host.get("kvm") or host.get("architecture") != "amd64":
                errors.append("native owner lacks root/KVM evidence")
        if any(len(value) != 1 for value in owner_hosts.values()):
            errors.append("native owner execution evidence missing on one or both hosts")
    except (OSError, ValueError, KeyError, TypeError):
        errors.append("native source/log/owner evidence missing or malformed")
    single = {}
    try:
        single = read_object(evidence / "single-host.json")
        if single.get("qualified") is not True or single.get("source_sha") != source_sha or single.get("run_id") != run_id:
            errors.append("matching full-daemon/provider reconciliation did not qualify")
        proofs = single["reconciliation"]
        if not proofs:
            errors.append("raw provider reconciliation proofs missing")
        for scenario, proof in proofs.items():
            if not re.fullmatch(r"[a-z_]+", scenario) or proof["sha256"] != digest(evidence / "reconciliation" / ("reconciliation-" + scenario + ".json")):
                errors.append("raw provider reconciliation proof changed")
        spec = importlib.util.spec_from_file_location("financial_system_ci_check", Path(__file__).with_name("financial-system-ci-check.py"))
        checker = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(checker)
        checked = checker.verify(root, evidence / "single-host", source_sha, run_id)
        if checked != single:
            errors.append("collected full-system report differs from its summary")
    except (OSError, ValueError, KeyError, TypeError):
        errors.append("full-daemon/provider verdict missing")
    return {
        "schema_version": 1, "scope": "physical_two_host_native_financial_components",
        "source_sha": source_sha, "run_id": run_id, "qualified": not errors,
        "promotion_ready": False,
        "hosts": hosts, "commands": status, "logs": logs, "sources": sources,
        "single_host": single, "failures": errors,
        "remaining_release_gates": [
            "cross-host gateway/owner response traffic and the complete custom-port/H2C/Upgrade/TCP/UDP matrix",
            "complete app/job/worker/sidecar/private workload matrix",
            "reference-SSD 100-cycle wake p95 and funded-egress throughput targets",
            "external provider sandbox/catalog reconciliation and complete native lifecycle gate",
        ],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("host", "cleanup", "verdict"))
    parser.add_argument("--bundle", type=Path)
    parser.add_argument("--root", type=Path)
    parser.add_argument("--evidence", type=Path)
    parser.add_argument("--source-sha")
    parser.add_argument("--run-id")
    for name in ("runner", "leakcheck", "post-repair", "restore"):
        parser.add_argument(f"--{name}-exit", type=int)
    args = parser.parse_args()
    if args.action == "host":
        write_json(args.bundle / "ready.json", host_report(args.bundle))
        return 0
    if args.action == "cleanup":
        source = host_report(args.bundle)
        write_json(args.bundle / "cleanup.json", {
            **source, **{name: getattr(args, name) for name in ("runner_exit", "leakcheck_exit", "post_repair_exit", "restore_exit")},
            "leakcheck_sha256": digest(args.bundle / "leakcheck.log"),
        })
        return 0
    report = build_report(args.root, args.evidence, args.source_sha, args.run_id)
    write_json(args.evidence / "qualification.json", report)
    print(f"financial pair: {'PASS' if report['qualified'] else 'FAIL'}; promotion_ready=false")
    for failure in report["failures"]:
        print("  " + failure, file=sys.stderr)
    return 0 if report["qualified"] else 1


if __name__ == "__main__":
    sys.exit(main())
