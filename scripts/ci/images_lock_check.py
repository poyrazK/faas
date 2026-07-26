#!/usr/bin/env python3
"""images_lock_check.py — Tier 3 (issue #197 B3.5 + B3.6) lock→Dockerfile gate.

Walks every entry in images/Dockerfile.lock and asserts that:

  1. No entry's `digest` is the `REPLACE_ME_AT_MERGE_TIME` placeholder.
     This is the "operator actually ran images-lock-update" gate — a
     PR that lands with placeholder digests will be rejected by CI
     before the squash-merge. Reported FIRST so a placeholder is a
     single clear error rather than a cascade.

  2. Each non-placeholder entry's `pinned_in_dockerfile` string is the
     exact FROM line in the named Dockerfile (whitespace tolerant). A
     Dockerfile that drifted from the lock (someone bumped a tag
     without re-running `make images-lock-update`) fails here.

This is the FORWARD direction of the lock — it answers "is the lock
honored?" The INVERSE direction ("is every FROM line in
images/*.Dockerfile covered by the lock?") lives in
`audit_dockerfile_froms.py` and is run as the second half of the
`images-lock-check` Makefile target. Together they form a two-sided
gate: the lock cannot drift forward (this file) and the Dockerfiles
cannot drift backward (the audit script).

Exits non-zero on any failure with a human-readable error. Pure stdlib
so CI doesn't need to install a JSON library.

Usage:
  ./scripts/ci/images_lock_check.py [--repo-root PATH] [--lock PATH]
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

PLACEHOLDER_DIGEST = "sha256:REPLACE_ME_AT_MERGE_TIME"


def check_lock(repo_root: Path, lock_path: Path) -> list[str]:
    """Return a list of error strings. Empty list = OK."""
    errors: list[str] = []
    if not lock_path.exists():
        return [f"lock file not found: {lock_path}"]

    try:
        lock = json.loads(lock_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as e:
        return [f"lock file is not valid JSON: {e}"]

    pinned = lock.get("pinned")
    if not isinstance(pinned, list) or not pinned:
        return ["lock.pinned is missing or empty"]

    for i, entry in enumerate(pinned):
        prefix = f"entry[{i}] {entry.get('dockerfile', '?')}"
        dockerfile_rel = entry.get("dockerfile")
        pinned_line = entry.get("pinned_in_dockerfile")
        digest = entry.get("digest")

        if not dockerfile_rel or not pinned_line or not digest:
            errors.append(
                f"{prefix}: missing one of (dockerfile, pinned_in_dockerfile, digest)"
            )
            continue

        # (1) Placeholder gate — must run BEFORE the file check so a
        # placeholder is reported as a single clear error.
        if digest == PLACEHOLDER_DIGEST:
            errors.append(
                f"{prefix}: digest is still {PLACEHOLDER_DIGEST}. "
                f"Run `make images-lock-update` to resolve the current "
                f"registry digest and update BOTH the lock and the "
                f"FROM line in {dockerfile_rel}."
            )
            # Skip the file comparison for placeholder entries; the
            # FROM line is also a placeholder by construction.
            continue

        dockerfile = repo_root / dockerfile_rel
        if not dockerfile.exists():
            errors.append(f"{prefix}: dockerfile {dockerfile} does not exist")
            continue

        # (2) The pinned_in_dockerfile string must appear as a line in
        # the Dockerfile. We match on stripped equality (the lock line
        # has no leading whitespace; the Dockerfile line may have none
        # either, but we strip both sides to be lenient about the
        # trailing newline).
        pinned_stripped = pinned_line.strip()
        try:
            contents = dockerfile.read_text(encoding="utf-8")
        except OSError as e:
            errors.append(f"{prefix}: read {dockerfile}: {e}")
            continue
        if not any(line.strip() == pinned_stripped for line in contents.splitlines()):
            errors.append(
                f"{prefix}: Dockerfile FROM line does not match lock. "
                f"Expected: {pinned_stripped!r}. "
                f"Either re-run `make images-lock-update` (if the new "
                f"digest is correct) or hand-edit the FROM line to "
                f"match the lock."
            )

    return errors


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument(
        "--repo-root",
        type=Path,
        default=Path(__file__).resolve().parent.parent.parent,
        help="Path to the repo root (default: parent of scripts/ci).",
    )
    ap.add_argument(
        "--lock",
        type=Path,
        default=None,
        help="Path to the lock file (default: <repo-root>/images/Dockerfile.lock).",
    )
    args = ap.parse_args(argv)

    lock_path = args.lock or (args.repo_root / "images" / "Dockerfile.lock")
    errors = check_lock(args.repo_root, lock_path)
    if errors:
        for e in errors:
            print(f"images-lock-check: FAIL: {e}", file=sys.stderr)
        return 1
    print("images-lock-check: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
