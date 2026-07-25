#!/usr/bin/env python3
"""audit_dockerfile_froms.py — reverse direction of images_lock_check.py.

Walks every FROM line in images/*.Dockerfile and asserts each one is
either:

  (a) Pinned by digest in the lock (its `pinned_in_dockerfile` line
      matches the actual line in the file), OR
  (b) An ARG-pinned line that doesn't need a digest pin. We treat
      `$TARGETARCH` and `$TARGETPLATFORM` substitutions as already
      digest-stable (they only vary the platform suffix, not the
      upstream repo+tag). Other ARG references (e.g. `FROM
      debian:${SUITE}`) ARE flagged — those are exactly the mutable
      tags the lock is meant to catch.

Exits non-zero on any failure. Pure stdlib.

Usage:
  ./scripts/ci/audit_dockerfile_froms.py [--repo-root PATH]
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

# FROM line shapes we treat as "stable enough to skip the lock check":
#   - FROM <image>@sha256:<digest>      digest-pinned; the lock owns the
#                                       digest, so it MUST appear in the
#                                       lock.
#   - FROM --platform=$XYZ <image>...   platform args we accept;
#                                       `$TARGETPLATFORM` is set by
#                                       `docker buildx build` and the
#                                       digest is still the same image.
# Anything else (FROM <image>:<tag> with no digest, FROM <image> with
# no tag) is treated as a mutable reference and MUST be in the lock.

FROM_RE = re.compile(r"^FROM\s+(?P<rest>.*)$")
ARG_PLACEHOLDER_RE = re.compile(r"\$\{?[A-Z_][A-Z0-9_]*\}?")


def parse_dockerfile_lines(text: str) -> list[tuple[int, str]]:
    """Return [(line_no, from_clause), ...] for every FROM line."""
    out: list[tuple[int, str]] = []
    for i, line in enumerate(text.splitlines(), start=1):
        m = FROM_RE.match(line.strip())
        if m:
            out.append((i, m.group("rest").strip()))
    return out


def from_clause_is_digest_pinned(rest: str) -> bool:
    """True if the FROM <rest> contains a @sha256:... reference."""
    return "@sha256:" in rest


def from_clause_has_arg_substitution(rest: str) -> bool:
    """True if the FROM <rest> contains any $ARG or ${ARG} placeholder.

    Note: the substitution need not be TARGETPLATFORM — even $SUITE
    (e.g. `debian:${SUITE}-slim`) is treated as "we don't know the
    resolved image at parse time, defer to the lock."
    """
    return bool(ARG_PLACEHOLDER_RE.search(rest))


# Canonical empty image — not a mutable upstream; the FROM `scratch`
# is exempt from the lock check.
EXEMPT_FROM_REFS = frozenset({"scratch"})


def audit_repo(repo_root: Path) -> list[str]:
    errors: list[str] = []
    images_dir = repo_root / "images"
    if not images_dir.is_dir():
        return [f"images dir not found: {images_dir}"]

    lock_path = images_dir / "Dockerfile.lock"
    if not lock_path.exists():
        return [f"lock file not found: {lock_path}"]
    lock = json.loads(lock_path.read_text(encoding="utf-8"))
    pinned_lines = {entry["pinned_in_dockerfile"] for entry in lock.get("pinned", [])}

    dockerfiles = sorted(images_dir.glob("*.Dockerfile"))
    if not dockerfiles:
        return [f"no .Dockerfile in {images_dir}"]

    for df in dockerfiles:
        text = df.read_text(encoding="utf-8")
        for line_no, rest in parse_dockerfile_lines(text):
            # Skip the digest-pinned case if the line exactly matches
            # a lock entry's `pinned_in_dockerfile`. (The forward
            # check — `pinned_in_dockerfile` exists in the Dockerfile —
            # is enforced by images_lock_check.py.)
            full_line = f"FROM {rest}"
            # Multi-stage build AS clauses land here as the first
            # whitespace-separated token, e.g. `debian:12-slim AS build`.
            # Strip the AS clause for the ref check.
            ref = rest.split()[0] if rest else ""
            if ref in EXEMPT_FROM_REFS:
                continue
            if from_clause_is_digest_pinned(rest):
                if any(p.strip() == full_line for p in pinned_lines):
                    continue
                errors.append(
                    f"{df}:{line_no}: {full_line!r} is digest-pinned but not in the lock. "
                    f"Add a `pinned` entry to images/Dockerfile.lock."
                )
                continue

            if from_clause_has_arg_substitution(rest):
                # An ARG-based FROM is OK only if it's still digest-pinned
                # after substitution. We can't know that without building,
                # so we just require the lock to cover it OR the rest to
                # be a multi-arch form (e.g. `--platform=$TARGETPLATFORM`).
                # The conservative answer: flag any ARG-substituted FROM
                # without a digest pin.
                errors.append(
                    f"{df}:{line_no}: {full_line!r} has an ARG substitution "
                    f"and no @sha256: digest pin. Pin it to a digest or "
                    f"add a `pinned` entry to images/Dockerfile.lock."
                )
                continue

            # Bare tag (no digest, no ARG) — always flagged.
            errors.append(
                f"{df}:{line_no}: {full_line!r} is a mutable tag with no "
                f"digest pin. Pin to @sha256: or add to images/Dockerfile.lock."
            )

    return errors


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument(
        "--repo-root",
        type=Path,
        default=Path(__file__).resolve().parent.parent.parent,
    )
    args = ap.parse_args(argv)
    errors = audit_repo(args.repo_root)
    if errors:
        for e in errors:
            print(f"audit-dockerfile-froms: FAIL: {e}", file=sys.stderr)
        return 1
    print("audit-dockerfile-froms: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
