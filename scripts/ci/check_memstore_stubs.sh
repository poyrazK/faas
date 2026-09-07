#!/usr/bin/env bash
# Fail when a MemStore method is reduced to a pure nil-return body.
#
# MemStore is a useful fast test backend only when an implemented method
# behaves like its PgStore counterpart. A newly added `return nil` body can
# make a handler test pass while never exercising the production path. The
# guard intentionally uses the Go source shape rather than a broad grep, so
# ordinary early-return branches remain valid.

set -euo pipefail

repo_root=${1:-$(git rev-parse --show-toplevel)}

python3 - "$repo_root" <<'PY'
import glob
import re
import sys

root = sys.argv[1]

# These methods are deliberate zero-value shims whose semantics are
# documented in memstore.go. They are not production data paths and are kept
# as explicit exceptions so the check still catches any newly introduced
# pure nil return.
allowed = {
    "HasSnapshotHistory",
    "LatestSnapshotBytes",
    "Ping",
    "UsageSLOForAccount",
    "UsageSLOForApp",
}

pattern = re.compile(
    r"func\s+\(m \*MemStore\)\s+(?P<name>\w+)\([^{}]*\)[^{]*\{"
    r"\s*return\s+(?P<values>[^\n{}]+?)\s*\}",
    re.S,
)

violations = []
for path in glob.glob(f"{root}/pkg/state/memstore*.go"):
    if path.endswith("_test.go"):
        continue
    source = open(path, encoding="utf-8").read()
    for match in pattern.finditer(source):
        values = match.group("values")
        parts = [part.strip() for part in values.split(",")]
        # Sentinel-return methods (for example, `return nil,
        # errMemStore...`) are explicit failures and are covered by their
        # own package tests. Only flag bodies whose complete result is made
        # from zero-value literals and nil.
        zero_values = {"nil", "0", "false", '""', "Deployment{}"}
        if (
            "nil" not in parts
            or match.group("name") in allowed
            or any(part not in zero_values for part in parts)
        ):
            continue
        line = source.count("\n", 0, match.start()) + 1
        violations.append((path, line, match.group("name"), values.strip()))

if violations:
    print("memstore stub check: pure nil-return methods found:", file=sys.stderr)
    for path, line, name, values in violations:
        print(f"  {path}:{line}: {name}: return {values}", file=sys.stderr)
    print(
        "Implement the method, or add a narrowly justified intentional "
        "shim to the allowlist in this script.",
        file=sys.stderr,
    )
    raise SystemExit(1)

print("memstore stub check: OK")
PY
