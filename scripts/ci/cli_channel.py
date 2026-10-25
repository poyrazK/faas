#!/usr/bin/env python3
"""Forward-only ordering for the curl installer's cli-channel pointers.

ADR-20261025092617: release.yml moves CLI-LATEST to every release tag and
CLI-STABLE to every tag without a prerelease suffix, each only forward by
semver precedence, so a re-run of an older tag cannot roll installs back.

Usage:
  cli_channel.py newer <candidate> [<current>]
      exit 0 when candidate takes precedence over current (or current is
      empty or unreadable), 1 when it does not
  cli_channel.py stable <tag>
      exit 0 when tag has no prerelease suffix, 1 when it has one

Any subcommand exits 2 when the candidate is not a release tag.
"""

import re
import sys

TAG = re.compile(
    r"^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)


def parse(tag):
    """Split a release tag into its numeric core and prerelease identifiers."""
    m = TAG.match(tag)
    if not m:
        raise ValueError(f"not a release tag: {tag!r}")
    core = tuple(int(part) for part in m.group(1, 2, 3))
    pre = m.group(4)
    return core, pre.split(".") if pre else None


def _identifier(ident):
    # Numeric identifiers compare numerically and below alphanumeric ones.
    return (0, int(ident), "") if ident.isdigit() else (1, 0, ident)


def newer(candidate, current):
    """Report whether candidate takes semver precedence over current."""
    cand_core, cand_pre = parse(candidate)
    if not current:
        return True
    try:
        cur_core, cur_pre = parse(current)
    except ValueError:
        return True  # an unreadable pointer is replaced
    if cand_core != cur_core:
        return cand_core > cur_core
    if cand_pre is None or cur_pre is None:
        return cand_pre is None and cur_pre is not None
    return [_identifier(i) for i in cand_pre] > [_identifier(i) for i in cur_pre]


def stable(tag):
    """Report whether tag has no prerelease suffix."""
    return parse(tag)[1] is None


def main(argv):
    if len(argv) < 2 or argv[0] not in ("newer", "stable"):
        print(__doc__, file=sys.stderr)
        return 2
    try:
        if argv[0] == "newer":
            return 0 if newer(argv[1], argv[2] if len(argv) > 2 else "") else 1
        return 0 if stable(argv[1]) else 1
    except ValueError as err:
        print(err, file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
