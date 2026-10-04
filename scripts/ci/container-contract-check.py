#!/usr/bin/env python3
"""Require portable container contracts to pass, including every subtest."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
PACKAGES = {
    "pkg/ingressroute": r"Test\w+",
    "pkg/tcpd": r"Test\w+",
    "pkg/oci": r"Test\w+",
    "pkg/ociidentity": r"Test\w+",
    "pkg/e2etest": r"Test(?:Hello|ImageVariants|Portable|FakeRegistry)\w*",
    "cmd/gregale": r"TestDoctorImage\w*",
    "pkg/imaged": r"Test(?:ApplyOverrides|ApplyAppLifecycle)\w*",
}


def required_tests(go):
    # go list selects source files for this platform/build configuration. Do
    # not require Linux-only or metal tests on a portable developer machine.
    result = subprocess.run([go, "list", "-json", *["./" + p for p in PACKAGES]],
                            cwd=ROOT, text=True, capture_output=True, check=False)
    if result.returncode:
        print(result.stderr, end="", file=sys.stderr)
        raise RuntimeError("container-contract-check: Go test source discovery failed")
    decoder = json.JSONDecoder()
    remaining = result.stdout
    expected = set()
    while remaining.strip():
        package, end = decoder.raw_decode(remaining.lstrip())
        remaining = remaining.lstrip()[end:]
        relative = str(Path(package["Dir"]).relative_to(ROOT))
        names = set()
        for filename in package.get("TestGoFiles", []) + package.get("XTestGoFiles", []):
            source = (Path(package["Dir"]) / filename).read_text()
            names.update(name for name in re.findall(
                r"^func\s+(Test\w+)\s*\(\s*(?:\w+\s+)?\*testing\.T\s*,?\s*\)", source, re.M)
                         if re.fullmatch(PACKAGES[relative], name))
        if not names:
            raise RuntimeError(f"container-contract-check: no required tests in {relative}")
        expected.update((package["ImportPath"], name) for name in names)
    if len({package for package, _ in expected}) != len(PACKAGES):
        raise RuntimeError("container-contract-check: missing package discovery")
    return expected


def main():
    go = os.environ.get("GO", "go")
    expected = required_tests(go)
    regex = "^(" + "|".join(sorted({name for _, name in expected})) + ")$"
    command = [go, "test", "-race", "-json", "-p", "1", "-count=1", "-timeout=5m", "-run", regex]
    command.extend("./" + package for package in PACKAGES)
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
            if action in ("skip", "fail"):
                rejected = True
                print(f"container-contract-check: {action}: {identity}", file=sys.stderr)
            if action in ("output", "build-output") and event.get("Output"):
                print(event["Output"], end="", flush=True)
        code = process.wait()
    missing = expected - passed
    for package, name in sorted(missing):
        print(f"container-contract-check: required pass missing: {package}/{name}", file=sys.stderr)
    if code or rejected or missing:
        return 1
    print(f"container-contract-check: all {len(expected)} portable contracts passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
