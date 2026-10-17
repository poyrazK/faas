#!/usr/bin/env python3
"""Verify the financial full-daemon lane, and retain its exact evidence hashes."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import stat
import sys
from datetime import datetime, timezone

SOURCES = {
    "native": "cmd/e2e/financial_system_metal_test.go",
    "provider": "pkg/e2etest/financialfixture/reconciliation_test.go",
}
BINARIES = ("apid", "schedd", "vmmd", "imaged", "gatewayd-internal", "gatewayd-public", "meterd")


def reconciliation_contract(root, records):
    """Independently check canonical integer provider events and receipts."""
    source = (root / SOURCES["provider"]).read_text()
    selected = re.search(r'for _, scenario := range \[\]string\{([^}]+)\}', source)
    if selected is None:
        raise ValueError("provider source selects no reconciliation scenarios")
    scenarios = re.findall(r'"([a-z_]+)"', selected[1])
    if not scenarios:
        raise ValueError("provider reconciliation scenarios missing")
    proofs = {}
    for scenario in scenarios:
        path = records / f"reconciliation-{scenario}.json"
        proof = json.loads(path.read_text())
        expected = proof["expected"]
        events = proof["provider"]["events"]
        if proof["scenario"] != scenario or not expected or expected != proof["receipts"] or set(events) != set(expected) or proof["provider"].get("conflicts"):
            raise ValueError(f"{scenario}: provider/receipt vectors do not reconcile")
        if any(type(proof.get(name)) is not int for name in ("invoice_millicents", "committed_millicents", "limit_millicents")):
            raise ValueError(f"{scenario}: financial amounts must be canonical integers")
        invoice = 0
        for key, quantity in expected.items():
            event = events[key]
            if type(quantity) is not int or type(proof["receipts"][key]) is not int or quantity <= 0 or event["external_id"] != key:
                raise ValueError(f"{scenario}: invalid canonical event identity/quantity")
            if event["name"] == "faas_ram_usage":
                field, unit, rate = "mb_seconds", 1024 * 3600, 1000
            elif event["name"] == "faas_egress_usage":
                field, unit, rate = "egress_bytes", 1 << 30, 2000
            else:
                raise ValueError(f"{scenario}: unexpected billed meter")
            if type(event["metadata"].get(field)) is not int or event["metadata"][field] != quantity:
                raise ValueError(f"{scenario}: canonical wire quantity changed")
            invoice += (quantity * rate + unit - 1) // unit
        if invoice != proof["invoice_millicents"] or not 0 <= invoice <= proof["committed_millicents"] <= proof["limit_millicents"]:
            raise ValueError(f"{scenario}: invoice exceeds its retained money")
        for meter in ("mb_seconds", "egress_bytes"):
            measured, funded = proof["measured_" + meter], proof["funded_" + meter]
            if type(measured) is not int or type(funded) is not int or not 0 < measured <= funded:
                raise ValueError(f"{scenario}: measured usage exceeds funded quantity")
        proofs[scenario] = {"path": str(path), "sha256": digest(path), "events": len(events), "invoice_millicents": invoice}
    return proofs


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def host_evidence():
    try:
        kvm = stat.S_ISCHR(Path("/dev/kvm").stat().st_mode)
    except OSError:
        kvm = False
    def hashed_identity(path):
        try:
            return hashlib.sha256(Path(path).read_bytes().strip()).hexdigest()
        except OSError:
            return None
    return {
        "hostname": platform.node(), "architecture": platform.machine(),
        "kernel": platform.release(), "root": os.geteuid() == 0, "kvm": kvm,
        "machine_id_sha256": hashed_identity("/etc/machine-id"),
        "boot_id_sha256": hashed_identity("/proc/sys/kernel/random/boot_id"),
    }


def inspect_lane(root, lane, log, exit_code):
    source = root / SOURCES[lane]
    errors = []
    source_text = ""
    result = {"exit_code": exit_code, "source": SOURCES[lane], "required": []}
    try:
        source_text = source.read_text()
        names = sorted(set(re.findall(r"^func (TestFinancialSystem\w+)\(", source_text, re.M)))
        result.update(source_sha256=digest(source), required=names)
        if not names:
            errors.append(f"{lane}: source selects no tests")
    except OSError:
        errors.append(f"{lane}: required source missing")
        names = []
    try:
        output = log.read_text()
        result.update(log=str(log), log_sha256=digest(log))
        if not re.search(r"^ok\s+github.com/onebox-faas/faas/", output, re.M):
            errors.append(f"{lane}: package did not finish successfully")
        if re.search(r"^\s*--- (?:FAIL|SKIP):", output, re.M):
            errors.append(f"{lane}: failed or skipped test/subtest")
        for name in names:
            if len(re.findall(rf"^--- PASS: {re.escape(name)}(?: |$)", output, re.M)) != 1:
                errors.append(f"{lane}: {name} must pass exactly once")
        # Native fault phases must run too: returning early from a passing
        # parent must never close a recovery gate. Derive their names from it.
        if lane == "native":
            phases = sorted(set(re.findall(r't\.Run\("([^"\n]+)"', source_text)))
            result["required_phases"] = phases
            for phase in phases:
                if not re.search(rf"^\s+--- PASS: TestFinancialSystem\w+/{re.escape(phase)}(?: |$)", output, re.M):
                    errors.append(f"{lane}: recovery phase {phase} absent")
    except OSError:
        errors.append(f"{lane}: required log missing")
    if type(exit_code) is not int or exit_code != 0:
        errors.append(f"{lane}: command exit was not zero")
    return result, errors


def build_report(args, host=None):
    host = host_evidence() if host is None else host
    errors = []
    if not re.fullmatch(r"[0-9a-f]{40}", args.source_sha):
        errors.append("reviewed source SHA is invalid")
    if not host.get("root") or not host.get("kvm") or host.get("architecture") != "x86_64":
        errors.append("dedicated native x86_64 KVM execution evidence missing")
    lanes = {}
    for lane in SOURCES:
        lanes[lane], lane_errors = inspect_lane(args.root, lane, getattr(args, lane + "_log"), getattr(args, lane + "_exit"))
        errors.extend(lane_errors)
    checks = {"leakcheck": args.leakcheck_exit, "service_restoration": args.restore_exit, "runner": args.runner_exit}
    for name, code in checks.items():
        if type(code) is not int or code != 0:
            errors.append(f"{name}: command exit was not zero")
    artifacts = {}
    for name, path in [(name, args.binary_root / name) for name in BINARIES] + [("guest_init", args.guest_init), ("kernel", args.kernel)]:
        try:
            if not path.is_file() or path.stat().st_size == 0:
                raise OSError("missing or empty")
            artifacts[name] = {"path": str(path), "sha256": digest(path), "bytes": path.stat().st_size}
        except OSError:
            errors.append(f"matching artifact missing: {name}")
    reconciliation = {}
    try:
        reconciliation = reconciliation_contract(args.root, args.provider_log.parent / "financial-system-records")
    except (OSError, ValueError, KeyError, TypeError):
        errors.append("canonical provider reconciliation evidence missing or invalid")
    return {
        "schema_version": 1, "scope": "single_host_full_daemon_and_provider_replay",
        "qualified": not errors, "source_sha": args.source_sha, "run_id": args.run_id,
        "recorded_at": datetime.now(timezone.utc).isoformat(), "host": host,
        "lanes": lanes, "checks": checks, "artifacts": artifacts, "reconciliation": reconciliation, "failures": errors,
        "remaining_release_gates": [
            "physical two-host contention and network partitions",
            "complete workload and offload matrix with performance targets",
            "external provider sandbox/catalog reconciliation",
            "complete native lifecycle suite and financial component lane",
        ],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--run-id", required=True)
    for lane in SOURCES:
        parser.add_argument(f"--{lane}-log", type=Path, required=True)
        parser.add_argument(f"--{lane}-exit", type=int, required=True)
    parser.add_argument("--leakcheck-exit", type=int, required=True)
    parser.add_argument("--restore-exit", type=int, required=True)
    parser.add_argument("--runner-exit", type=int, required=True)
    parser.add_argument("--binary-root", type=Path, required=True)
    parser.add_argument("--guest-init", type=Path, required=True)
    parser.add_argument("--kernel", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    report = build_report(args)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = args.output.with_suffix(".tmp")
    temporary.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    temporary.replace(args.output)
    print(f"financial-system: {'PASS' if report['qualified'] else 'FAIL'}; evidence={args.output}")
    for error in report["failures"]:
        print(f"  {error}", file=sys.stderr)
    return 0 if report["qualified"] else 1


if __name__ == "__main__":
    sys.exit(main())
