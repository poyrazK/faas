#!/usr/bin/env python3
"""ADR-521: combine cross-package Go coverage without counting blocks twice."""

from pathlib import Path
import re
import sys


def normalize(profile: str) -> str:
    lines = profile.splitlines()
    if not lines or lines[0] not in {"mode: set", "mode: count", "mode: atomic"}:
        raise ValueError("missing or unsupported Go coverage mode")
    mode = lines[0]
    blocks: dict[str, tuple[int, int]] = {}
    for line in lines[1:]:
        if not line:
            continue
        location, statements_raw, hits_raw = line.rsplit(" ", 2)
        if not re.fullmatch(r".+:\d+\.\d+,\d+\.\d+", location):
            raise ValueError(f"invalid coverage location: {location}")
        statements, hits = int(statements_raw), int(hits_raw)
        if statements < 0 or hits < 0:
            raise ValueError(f"negative coverage value: {line}")
        prior_statements, prior_hits = blocks.get(location, (statements, 0))
        if prior_statements != statements:
            raise ValueError(f"conflicting statement count: {location}")
        total = prior_hits + hits
        blocks[location] = (statements, min(total, 1) if mode == "mode: set" else total)
    return mode + "\n" + "".join(
        f"{location} {statements} {hits}\n"
        for location, (statements, hits) in sorted(blocks.items())
    )


def main() -> None:
    path = Path(sys.argv[1])
    normalized = normalize(path.read_text(encoding="utf-8"))
    temporary = path.with_name(path.name + ".normalized")
    temporary.write_text(normalized, encoding="utf-8")
    temporary.replace(path)


if __name__ == "__main__":
    main()
