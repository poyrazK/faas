#!/usr/bin/env python3
"""images_lock_refresh.py — hand drifted base-image digests to a reviewer.

ADR 20261024132405 decision 2. Runs after images_lock_update.py has rewritten
images/Dockerfile.lock and the matching FROM lines. When no digest moved it
restores the tree (the resolver always stamps `generated_at`) and exits.
Otherwise it commits the change to a new
`automation/images-lock-refresh-<date>-<run id>` branch, pushes it, and opens
or comments on one tracking issue listing the moved bases, with a link that
opens the pull request.

The repository's Actions token may not open pull requests, and a pull request
opened by that token would not start CI, so an engineer opens it from the
link. images.yml then builds, smoke-tests and scans the new bases as usual.

Usage:
  ./scripts/ci/images_lock_refresh.py [--repo-root PATH] [--dry-run]

Environment: GITHUB_REPOSITORY (required), GITHUB_RUN_ID, and GH_TOKEN for
the gh CLI. IMAGES_LOCK_REFRESH_DATE overrides the branch date (tests).
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import subprocess
import sys
import urllib.parse
from pathlib import Path

LOCK = "images/Dockerfile.lock"
ISSUE_TITLE = "Base image digests drifted"
PR_TITLE = "build(images): refresh pinned base image digests"
BOT = ("github-actions[bot]", "41898282+github-actions[bot]@users.noreply.github.com")


def run(args: list[str], cwd: Path, capture: bool = False) -> str:
    result = subprocess.run(args, cwd=cwd, check=True, text=True,
                            stdout=subprocess.PIPE if capture else None)
    return result.stdout if capture else ""


def moved_entries(old: dict, new: dict) -> list[tuple[str, str, str, str]]:
    """Return (dockerfile, alias, old digest, new digest) per moved pin."""
    moved = []
    seen = set()
    for before, after in zip(old["pinned"], new["pinned"]):
        if before["digest"] == after["digest"]:
            continue
        key = (after["dockerfile"], after["alias"])
        if key in seen:
            continue
        seen.add(key)
        moved.append((after["dockerfile"], after["alias"], before["digest"], after["digest"]))
    return moved


def short(digest: str) -> str:
    return digest.removeprefix("sha256:")[:12]


def issue_body(repo: str, branch: str, moved: list[tuple[str, str, str, str]]) -> str:
    query = urllib.parse.urlencode({"expand": "1", "title": PR_TITLE})
    compare = f"https://github.com/{repo}/compare/main...{branch}?{query}"
    rows = "\n".join(
        f"| `{dockerfile}` | `{alias}` | `{short(old)}` | `{short(new)}` |"
        for dockerfile, alias, old, new in moved
    )
    return (
        f"The weekly base-image refresh found {len(moved)} pinned base(s) whose "
        f"upstream tag moved. Branch `{branch}` carries the new digests.\n\n"
        f"**[Open the pull request]({compare})** — images.yml builds, "
        "smoke-tests and scans the new bases before merge "
        "(ADR 20261024132405).\n\n"
        "| Dockerfile | Base | Old | New |\n|---|---|---|---|\n"
        f"{rows}\n"
    )


def file_issue(repo_root: Path, body: str) -> None:
    listed = run(["gh", "issue", "list", "--state", "open", "--search",
                  f"{ISSUE_TITLE} in:title", "--json", "number,title"],
                 repo_root, capture=True)
    existing = [i["number"] for i in json.loads(listed or "[]") if i["title"] == ISSUE_TITLE]
    if existing:
        run(["gh", "issue", "comment", str(existing[0]), "--body", body], repo_root)
    else:
        run(["gh", "issue", "create", "--title", ISSUE_TITLE, "--label", "dependencies",
             "--body", body], repo_root)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo-root", type=Path, default=Path.cwd())
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    root = args.repo_root
    repo = os.environ.get("GITHUB_REPOSITORY")
    if not repo:
        print("images-lock-refresh: GITHUB_REPOSITORY is required", file=sys.stderr)
        return 2

    old = json.loads(run(["git", "show", f"HEAD:{LOCK}"], root, capture=True))
    new = json.loads((root / LOCK).read_text(encoding="utf-8"))
    moved = moved_entries(old, new)
    # Only the lock and the Dockerfiles it pins are the resolver's to touch.
    touched = sorted({LOCK, *(entry["dockerfile"] for entry in new["pinned"])})
    if not moved:
        run(["git", "checkout", "--", *touched], root)
        print("images-lock-refresh: every pinned base is current")
        return 0

    stamp = os.environ.get("IMAGES_LOCK_REFRESH_DATE") or dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d")
    branch = f"automation/images-lock-refresh-{stamp}-{os.environ.get('GITHUB_RUN_ID', 'local')}"
    body = issue_body(repo, branch, moved)
    if args.dry_run:
        print(f"images-lock-refresh: dry run, would push {branch}\n\n{body}")
        return 0

    summary = "\n".join(f"- {alias} ({dockerfile}): {short(o)} -> {short(n)}"
                        for dockerfile, alias, o, n in moved)
    run(["git", "switch", "--create", branch], root)
    run(["git", "add", "--", *touched], root)
    run(["git", "-c", f"user.name={BOT[0]}", "-c", f"user.email={BOT[1]}", "commit",
         "--quiet", "-m", PR_TITLE, "-m", summary], root)
    run(["git", "push", "--quiet", "origin", branch], root)
    file_issue(root, body)
    print(f"images-lock-refresh: pushed {branch} ({len(moved)} moved)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
