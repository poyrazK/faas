#!/usr/bin/env python3
"""Require every third-party GitHub Action to be pinned to a commit SHA.

A tag (`actions/checkout@v7`) can be moved to new code by whoever controls the
action's repository, and the next workflow run executes it, including on the
self-hosted CD runners that hold deploy credentials. Pins look like
`owner/repo[/path]@<40-hex> # vN`; the trailing tag is what Dependabot reads
to keep them current. Local actions (`./...`) and this repository's own
published actions (`poyrazK/faas/...`) are exempt.
"""
from __future__ import annotations

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
USES = re.compile(r"^\s*-?\s*uses:\s*[\"']?([^\s\"'#]+)")
PINNED = re.compile(r"^[^@\s]+@[0-9a-f]{40}$")
EXEMPT_PREFIXES = ("./", "poyrazK/faas/", "docker://")


def unpinned(root: pathlib.Path) -> list[str]:
    found = []
    files = sorted(root.glob(".github/workflows/*.yml")) + sorted(root.glob(".github/workflows/*.yaml"))
    files += sorted(root.glob(".github/actions/**/action.yml")) + sorted(root.glob(".github/actions/**/action.yaml"))
    for path in files:
        for number, line in enumerate(path.read_text().splitlines(), 1):
            m = USES.match(line)
            if not m:
                continue
            ref = m.group(1)
            if ref.startswith(EXEMPT_PREFIXES) or PINNED.match(ref):
                continue
            found.append(f"{path.relative_to(root)}:{number}: {ref}")
    return found


def main() -> int:
    found = unpinned(ROOT)
    if found:
        print("third-party actions must be pinned to a commit SHA (`owner/repo@<sha> # vN`):", file=sys.stderr)
        for line in found:
            print(f"  {line}", file=sys.stderr)
        return 1
    print("action pin check: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
