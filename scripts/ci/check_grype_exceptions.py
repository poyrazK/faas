#!/usr/bin/env python3
"""check_grype_exceptions.py — ADR 20261024132405 decision 3 gate.

Every images/<image>.grype.yaml must:

  1. Name an image that exists (images/<image>.Dockerfile), so an exception
     file cannot outlive the lane it was written for.
  2. Pin every ignore rule to an exact package: `name`, `version` and `type`.
     The version pin makes a rule lapse when the image moves to the next
     release, which brings the finding back for review.
  3. Give every rule a non-empty `reason` and a CVE or GHSA identifier.

The files use one fixed shape (see images/runner-python312.grype.yaml), so a
line-based reader is enough and CI needs no YAML library.

Usage:
  ./scripts/ci/check_grype_exceptions.py [--images-dir PATH]
"""
from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

ENTRY = re.compile(r"^  - vulnerability:\s*(\S+)\s*$")
VULN_ID = re.compile(r"^(CVE-\d{4}-\d{4,}|GHSA(-[0-9a-z]{4}){3})$")
REASON = re.compile(r"^    reason:\s*(.*)$")
PACKAGE = re.compile(r"^    package:\s*$")
PACKAGE_FIELD = re.compile(r"^      (name|version|type):\s*(\S.*)?$")
FOLDED = {">", ">-", "|", "|-"}


def check_entry(path: Path, vuln: str, body: list[str]) -> list[str]:
    errors = []
    where = f"{path.name}: {vuln}"
    if not VULN_ID.match(vuln):
        errors.append(f"{where}: vulnerability must be a CVE or GHSA id")
    reason = ""
    package: dict[str, str] = {}
    key = None
    for line in body:
        if m := REASON.match(line):
            key = "reason"
            value = m.group(1).strip()
            reason = "" if value in FOLDED else value.strip("\"'")
        elif PACKAGE.match(line):
            key = "package"
        elif key == "package" and (m := PACKAGE_FIELD.match(line)):
            package[m.group(1)] = (m.group(2) or "").strip()
        elif key == "reason" and line.startswith("      "):
            reason = f"{reason} {line.strip()}".strip()
        else:
            errors.append(f"{where}: unexpected line {line!r}")
    if not reason:
        errors.append(f"{where}: needs a non-empty reason")
    for field in ("name", "version", "type"):
        if not package.get(field):
            errors.append(f"{where}: package.{field} must pin the exact package")
    return errors


def check_file(path: Path, images_dir: Path) -> list[str]:
    errors = []
    image = path.name.removesuffix(".grype.yaml")
    if not (images_dir / f"{image}.Dockerfile").is_file():
        errors.append(f"{path.name}: no images/{image}.Dockerfile for this exception file")
    lines = [
        line
        for line in path.read_text().splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    ]
    if lines == ["ignore: []"]:
        return errors
    if not lines or lines[0] != "ignore:":
        return errors + [f"{path.name}: must hold one top-level `ignore:` list"]
    entries: list[tuple[str, list[str]]] = []
    for line in lines[1:]:
        if m := ENTRY.match(line):
            entries.append((m.group(1), []))
        elif entries and line.startswith("    "):
            entries[-1][1].append(line)
        else:
            errors.append(f"{path.name}: unexpected line {line!r}")
    if not entries:
        errors.append(f"{path.name}: `ignore:` has no rules; use `ignore: []`")
    for vuln, body in entries:
        errors.extend(check_entry(path, vuln, body))
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--images-dir",
        type=Path,
        default=Path(__file__).resolve().parents[2] / "images",
    )
    args = parser.parse_args()
    errors = []
    files = sorted(args.images_dir.glob("*.grype.yaml"))
    for path in files:
        errors.extend(check_file(path, args.images_dir))
    for error in errors:
        print(f"grype-exceptions: {error}", file=sys.stderr)
    if errors:
        return 1
    print(f"grype-exceptions: OK ({len(files)} files)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
