#!/usr/bin/env python3
"""Reject duplicate GitHub Release asset names across publishing jobs."""

from __future__ import annotations

import pathlib
import re
import sys


def release_assets(workflow: pathlib.Path) -> list[tuple[str, str]]:
    assets: list[tuple[str, str]] = []
    job = "unknown"
    release_step = False
    files_indent: int | None = None

    for raw in workflow.read_text(encoding="utf-8").splitlines():
        stripped = raw.strip()
        indent = len(raw) - len(raw.lstrip())

        job_match = re.match(r"^  ([A-Za-z0-9_-]+):\s*$", raw)
        if job_match:
            job = job_match.group(1)

        if indent == 6 and stripped.startswith("- "):
            release_step = False
            files_indent = None

        if "uses: softprops/action-gh-release@" in stripped:
            release_step = True
            continue

        if release_step and stripped == "files: |":
            files_indent = indent
            continue

        if files_indent is None:
            continue
        if not stripped or stripped.startswith("#"):
            continue
        if indent <= files_indent:
            files_indent = None
            continue

        assets.append((pathlib.PurePosixPath(stripped).name, job))

    return assets


def main() -> int:
    workflow = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else ".github/workflows/release.yml")
    seen: dict[str, str] = {}
    duplicates: list[str] = []
    assets = release_assets(workflow)

    for name, job in assets:
        if name in seen:
            duplicates.append(f"{name}: {seen[name]} and {job}")
        else:
            seen[name] = job

    if duplicates:
        print("duplicate GitHub Release asset names:", file=sys.stderr)
        for duplicate in duplicates:
            print(f"  {duplicate}", file=sys.stderr)
        return 1

    print(f"release asset names unique: {len(assets)} assets across {len(set(job for _, job in assets))} jobs")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
