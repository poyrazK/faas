#!/usr/bin/env python3
"""Fail if the compiler compatibility update drops an existing security rule."""

from pathlib import Path
import re
import sys


PREEXISTING_EXCLUSIONS = set("G101 G115 G204 G301 G302 G304 G306 G404".split())


def missing_baseline_rules(config, registry):
    settings = re.search(r"(?ms)^    gosec:\n(.*?)(?=^ {0,4}\S|\Z)", config)
    if not settings:
        raise ValueError("missing gosec configuration")

    def rules(name):
        section = re.search(r"(?m)^ {6}" + name + r":\n((?: {8}[^\n]*\n)+)", settings[1])
        if not section:
            raise ValueError("missing explicit gosec " + name)
        return set(re.findall(r"(?m)^ {8}- (G\d{3})\b", section[1]))

    active = rules("includes") - rules("excludes")
    return (set(registry) - PREEXISTING_EXCLUSIONS) - active


def main():
    root = Path(__file__).resolve().parents[2]
    registry = [line for line in (root / "scripts/ci/gosec_baseline_rules.txt").read_text().splitlines()
                if re.fullmatch(r"G\d{3}", line)]
    if len(registry) != 40 or len(set(registry)) != 40:
        print("invalid pre-upgrade security rule inventory", file=sys.stderr)
        return 1
    try:
        missing = missing_baseline_rules((root / ".golangci.yml").read_text(), registry)
    except ValueError as error:
        print(error, file=sys.stderr)
        return 1
    if missing:
        print("Existing security rules disabled: " + ", ".join(sorted(missing)), file=sys.stderr)
        return 1
    print("Existing Go security rules: all 32 retained")
    return 0


if __name__ == "__main__":
    sys.exit(main())
