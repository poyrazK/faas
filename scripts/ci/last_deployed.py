#!/usr/bin/env python3
"""The newest commit a unit release deployed through every lane it ran.

docs/adr/20261122170000-continuous-unit-lane.md, "Blast cap". Every
release-units run that releases a commit uploads the marker artifact
`unit-release-sha-<commit>` (deploy_preflight.RELEASE_MARKER). The newest
such run whose lanes all passed (deploy_preflight.lanes_passed) names the
commit production last took cleanly. release-units caps a release at
`--cap` first-parent commits beyond it, and releases nothing when the
selected commit is that one.

Prints the commit, or nothing when no marker run passed (the hosts run the
full release, which the caller then counts from).

Usage:
  GH_TOKEN=... scripts/ci/last_deployed.py [--repo owner/name] [--pages 3]
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


def last_deployed(gh, pages: int = 3) -> str | None:
    """The released commit of the newest marker run whose lanes passed."""
    markers = []
    for page in range(1, pages + 1):
        arts = gh.get("actions/artifacts", {"per_page": 100, "page": page}).get("artifacts", [])
        markers += [a for a in arts if a.get("name", "").startswith(dp.RELEASE_MARKER) and not a.get("expired")]
        if len(arts) < 100:
            break
    markers.sort(key=lambda a: a.get("created_at", ""), reverse=True)
    seen = set()
    for art in markers:
        run_id = (art.get("workflow_run") or {}).get("id")
        if run_id is None or run_id in seen:
            continue
        seen.add(run_id)
        jobs = gh.get(f"actions/runs/{run_id}/jobs", {"per_page": 100}).get("jobs", [])
        if dp.lanes_passed(jobs):
            return art["name"][len(dp.RELEASE_MARKER):]
    return None


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY", ""))
    ap.add_argument("--pages", type=int, default=3)
    args = ap.parse_args(argv)
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN") or ""
    if not args.repo or not token:
        print("last_deployed: needs --repo and GH_TOKEN", file=sys.stderr)
        return 2
    try:
        sha = last_deployed(dp.GitHub(args.repo, token), args.pages)
    except (OSError, ValueError, urllib.error.URLError) as err:
        # Unknown is treated as "count from the full release": a smaller
        # step, never a larger one.
        print(f"warning: cannot read earlier releases: {err}", file=sys.stderr)
        sha = None
    print(sha or "")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
