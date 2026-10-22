#!/usr/bin/env python3
"""Pause automatic deploys after an automatic rollback: open a GitHub issue
labelled deploy-freeze
(docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md).

Unit releases accumulate over their base full release, so the next merge's
release contains the change that was just rolled back. The deploy preflight
(deploy_preflight.py) holds every automatic deploy while an open issue
carries the label. A person closes it once the cause is reverted or fixed.

  deploy_freeze.py --title TITLE --body-file FILE

The label is created when the repository lacks it. Prints the issue URL.
Needs GH_TOKEN with issues: write and GITHUB_REPOSITORY.
"""
from __future__ import annotations

import argparse
import importlib.util
import os
import pathlib
import sys
import urllib.error

HERE = pathlib.Path(__file__).parent


def _preflight():
    module = sys.modules.get("deploy_preflight")
    if module is None:
        spec = importlib.util.spec_from_file_location("deploy_preflight", HERE / "deploy_preflight.py")
        module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
    return module


dp = _preflight()
LABEL = dp.FREEZE_LABEL
LABEL_COLOR = "b60205"
LABEL_DESCRIPTION = "Holds automatic per-unit deploys (deploy preflight); close to lift"
FOOTER = (
    "\n\n---\n"
    f"While this issue is open with the `{LABEL}` label, automatic per-unit deploys are held "
    "(docs/runbooks/DeployFreeze.md). Close it once the cause is reverted or fixed; the next merge "
    "then deploys everything merged since."
)


def ensure_label(gh) -> None:
    try:
        gh.get(f"labels/{LABEL}")
    except urllib.error.HTTPError as err:
        err.close()
        if err.code != 404:
            raise
        gh.post("labels", {"name": LABEL, "color": LABEL_COLOR, "description": LABEL_DESCRIPTION})


def open_freeze_issue(gh, title: str, body: str) -> str:
    ensure_label(gh)
    issue = gh.post("issues", {"title": title[:240], "body": body + FOOTER, "labels": [LABEL]})
    return issue["html_url"]


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--title", required=True)
    parser.add_argument("--body-file", required=True)
    args = parser.parse_args(argv)
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN", "")
    repo = os.environ.get("GITHUB_REPOSITORY", "")
    if not token or not repo:
        print("::error::deploy_freeze needs GH_TOKEN and GITHUB_REPOSITORY", file=sys.stderr)
        return 2
    body = pathlib.Path(args.body_file).read_text(encoding="utf-8")
    try:
        url = open_freeze_issue(dp.GitHub(repo, token), args.title, body)
    except (OSError, urllib.error.URLError, KeyError) as err:
        print(f"::error::could not open the deploy-freeze issue: {err}; freeze deploys by hand "
              "(docs/runbooks/DeployFreeze.md)", file=sys.stderr)
        return 1
    print(url)
    print(f"::warning::automatic deploys are paused: {url}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
