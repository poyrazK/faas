#!/usr/bin/env python3
"""Reject compiled executables committed to the repository (ADR-131).

A stray `go build` output at the repo root (`sdk-coverage`, a Mach-O built
with go1.25.13) carried 13 of the 24 findings the nightly CVE monitor
reported on 2026-10-21. Every tracked file that starts with an ELF, Mach-O or
PE header fails this check unless ALLOWED names it with the gate that keeps
it honest.
"""

from __future__ import annotations

import pathlib
import struct
import subprocess
import sys


ROOT = pathlib.Path(__file__).resolve().parents[2]

# Intentional binaries, each with the check that proves it is current.
ALLOWED = {
    "guest/init/rngpreload/reseed.node":
        "Node RNG reseed addon embedded by guest-init; `make rng-addon-check` "
        "proves it is the reproducible build of reseed.c (ADR-687)",
}

MACHO_MAGIC = {
    b"\xfe\xed\xfa\xce", b"\xce\xfa\xed\xfe",  # 32-bit, both byte orders
    b"\xfe\xed\xfa\xcf", b"\xcf\xfa\xed\xfe",  # 64-bit, both byte orders
}


def executable_kind(head: bytes) -> str | None:
    if head[:4] == b"\x7fELF":
        return "ELF"
    if head[:4] in MACHO_MAGIC:
        return "Mach-O"
    # Universal binaries share 0xcafebabe with Java class files; a fat header
    # carries a small architecture count where a class file has its version.
    if head[:4] == b"\xca\xfe\xba\xbe" and len(head) >= 8 and 0 < struct.unpack(">I", head[4:8])[0] < 20:
        return "Mach-O universal"
    if head[:2] == b"MZ" and len(head) >= 64:
        offset = struct.unpack("<I", head[60:64])[0]
        if head[offset:offset + 4] == b"PE\0\0":
            return "PE"
    return None


def tracked_files(root: pathlib.Path) -> list[str]:
    out = subprocess.run(["git", "-C", str(root), "ls-files", "-z"], check=True, capture_output=True).stdout
    return [name for name in out.decode().split("\0") if name]


def violations(root: pathlib.Path, allowed: dict[str, str]) -> list[str]:
    found: list[str] = []
    for name in tracked_files(root):
        path = root / name
        if path.is_symlink() or not path.is_file():
            continue
        with path.open("rb") as handle:
            head = handle.read(1024)
        kind = executable_kind(head)
        if kind and name not in allowed:
            found.append(f"{name}: {kind} executable")
    return found


def main() -> int:
    found = violations(ROOT, ALLOWED)
    if found:
        print("committed executables (delete them, or add a justified entry to ALLOWED in "
              "scripts/ci/check_committed_binaries.py):", file=sys.stderr)
        for line in found:
            print(f"  {line}", file=sys.stderr)
        return 1
    print("committed-binaries-check: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
