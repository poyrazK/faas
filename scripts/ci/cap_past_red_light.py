#!/usr/bin/env python3
"""Move a capped unit release off a commit whose light tier failed.

docs/adr/20261122170000-continuous-unit-lane.md, "Blast cap". The cap
picks the Nth first-parent commit beyond what production last took. The
deploy preflight refuses a release whose commit failed main's post-merge
light tier, and every later run picks the same capped commit, so one red
commit inside the window would hold the lane until a full release. Units
releases are cumulative, so the release moves forward to the first later
commit whose light tier did not fail (it carries the fix as well).

Arguments: the capped commit, then the later first-parent commits up to
the uncapped target, oldest first. Prints the commit to release: the first
argument whose light tier did not fail, or the capped commit when all
failed or GitHub cannot be read (the preflight then refuses, as before).

Usage:
  GH_TOKEN=... scripts/ci/cap_past_red_light.py [--repo owner/name] CAPPED [LATER...]
"""
from __future__ import annotations

import argparse
import importlib.util
import os
import pathlib
import sys
import urllib.error

HERE = pathlib.Path(__file__).parent
_spec = importlib.util.spec_from_file_location("deploy_preflight", HERE / "deploy_preflight.py")
dp = importlib.util.module_from_spec(_spec)
sys.modules[_spec.name] = dp
_spec.loader.exec_module(dp)


def past_red_light(gh, commits: list[str]) -> str:
    """The first commit whose light tier did not fail; commits[0] if none."""
    for sha in commits:
        if not any(dp.light_run_failed(r) for r in dp.light_runs(gh, sha)):
            return sha
    return commits[0]


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY", ""))
    ap.add_argument("commits", nargs="+")
    args = ap.parse_args(argv)
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN") or ""
    if not args.repo or not token:
        print(args.commits[0])
        return 0
    try:
        sha = past_red_light(dp.GitHub(args.repo, token), args.commits)
    except (OSError, ValueError, urllib.error.URLError) as err:
        print(f"warning: cannot read light tier runs: {err}", file=sys.stderr)
        sha = args.commits[0]
    print(sha)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
