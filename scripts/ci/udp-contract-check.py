#!/usr/bin/env python3
"""Portable UDP contracts; skipped or missing cases cannot count as passes."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
FILES = {
    "pkg/ingressroute": "*_test.go",
    "pkg/udpd": "*_test.go",
    "pkg/udpwire": "*_test.go",
    "cmd/vmmd-udp-bridge": "main_test.go",
    "pkg/gateway": "udpforward_test.go",
    "pkg/vmmdgrpc": ["forward_udp_test.go", "forward_udp_grpc_test.go", "bridge_readiness_test.go"],
    "cmd/apid": "handlers_udp_listeners_test.go",
    "pkg/api": "client_udp_listeners_test.go",
    "cmd/gregale": "commands_app_udp_test.go",
    "cmd/gatewayd-public": "udp_ingress_test.go",
    "pkg/e2etest": "gateway_public_env_test.go",
    "pkg/state": ["memstore_udp_listeners_test.go", "listener_app_purge_test.go"],
    "api/proto/onebox/faas/vmmd/v1": "udp_contract_test.go",
}


def required_tests():
    expected = set()
    module = re.search(r"^module (\S+)", (ROOT / "go.mod").read_text(), re.M).group(1)
    for package, patterns in FILES.items():
        files = []
        for pattern in ([patterns] if isinstance(patterns, str) else patterns):
            selected = sorted((ROOT / package).glob(pattern))
            if not selected:
                raise SystemExit(f"udp-contract-check: missing test sources: {package}/{pattern}")
            files.extend(selected)
        for source in files:
            names = re.findall(r"^func\s+(Test\w+)\s*\(\s*(?:\w+\s+)?\*testing\.T\s*,?\s*\)", source.read_text(), re.M)
            if not names:
                raise SystemExit(f"udp-contract-check: no tests selected from {source}")
            expected.update((f"{module}/{package}", name) for name in names)
    return expected


def main():
    expected = required_tests()
    regex = "^(" + "|".join(sorted({name for _, name in expected})) + ")$"
    command = [os.environ.get("GO", "go"), "test", "-race", "-json", "-p", "1",
               "-count=1", "-timeout=3m", "-run", regex]
    command.extend("./" + package for package in FILES)
    passed = set()
    rejected = False
    with subprocess.Popen(command, cwd=ROOT, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, text=True) as process:
        for line in process.stdout:
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                print(line, end="", flush=True)
                continue
            identity = (event.get("Package"), event.get("Test"))
            action = event.get("Action")
            if action == "pass" and identity in expected:
                passed.add(identity)
                print(f"PASS {identity[0]}/{identity[1]}", flush=True)
            if action in ("skip", "fail"):
                rejected = True
                print(f"udp-contract-check: {action}: {identity}", file=sys.stderr)
            if action in ("output", "build-output") and event.get("Output"):
                print(event["Output"], end="", flush=True)
        code = process.wait()
    missing = expected - passed
    for package, name in sorted(missing):
        print(f"udp-contract-check: required pass missing: {package}/{name}", file=sys.stderr)
    if code or rejected or missing:
        return 1
    print(f"udp-contract-check: all {len(expected)} portable contracts passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
