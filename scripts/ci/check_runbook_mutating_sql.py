#!/usr/bin/env python3
"""Reject ad-hoc mutating SQL in normal operator documentation."""

from __future__ import annotations

import pathlib
import re
import sys


ROOT = pathlib.Path(__file__).resolve().parents[2]
DOC_ROOTS = (ROOT / "docs" / "runbooks", ROOT / "docs" / "ops")
MUTATION = re.compile(
    r"\b(?:"
    r"INSERT\s+INTO\s+[a-z_][a-z0-9_.]*|"
    r"UPDATE\s+[a-z_][a-z0-9_.]*\s+SET\b|"
    r"DELETE\s+FROM\s+[a-z_][a-z0-9_.]*|"
    r"ALTER\s+TABLE\s+[a-z_][a-z0-9_.]*|"
    r"DROP\s+(?:TABLE|INDEX)\s+(?:CONCURRENTLY\s+)?(?:IF\s+EXISTS\s+)?[a-z_][a-z0-9_.]*|"
    r"TRUNCATE[ \t]+(?:TABLE[ \t]+)?(?:ONLY[ \t]+)?[a-z_][a-z0-9_.]*"
    r"(?:[ \t]+(?:CASCADE|RESTRICT))?[ \t]*(?=;|$)"
    r")",
    re.IGNORECASE | re.MULTILINE,
)


def line_number(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def main() -> int:
    violations: list[str] = []
    for doc_root in DOC_ROOTS:
        for path in sorted(doc_root.rglob("*.md")):
            text = path.read_text(encoding="utf-8")
            for match in MUTATION.finditer(text):
                line = line_number(text, match.start())
                statement = " ".join(match.group(0).split())
                violations.append(f"{path.relative_to(ROOT)}:{line}: {statement}")
    if violations:
        print("mutating SQL is forbidden in normal runbooks; move reviewed emergency SQL to docs/break-glass/", file=sys.stderr)
        print("\n".join(violations), file=sys.stderr)
        return 1
    print("runbook SQL gate: no mutating SQL outside docs/break-glass/")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
