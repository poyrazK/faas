#!/usr/bin/env python3
"""Weekly rollback drill: can an automatic rollback still happen?

docs/adr/20261122170000-continuous-unit-lane.md, "Rollback drill". The
drill changes nothing in production: breaking production to prove it
recovers would be the opposite of the lane's goal. It checks every fact an
automatic rollback depends on, from the control plane
(`unit_deploy.sh rollback-readiness`, `preflight-state`) and GitHub:

  active      /opt/faas/current resolves to a release
  history     deployctl's activation history ends with that release, so
              `deployctl rollback` knows what to restore
  predecessor the release before it is still installed (manifest, bin/)
  known-good  the watch record's known-good control-plane release, when
              there is a record, is still installed
  host-config the control plane's host-config record exists, so a breach can
              converge the roles back
  prometheus  the control plane's Prometheus answers: the SLO gate and the
              watch read it
  freeze      the deploy-freeze label exists, so a breach can pause deploys

A failed check fails the run and opens an issue (not a freeze: nothing is
broken yet, but the next breach could not be undone automatically).

Usage:
  GH_TOKEN=... scripts/ci/rollback_drill.py [--repo owner/name] [--summary FILE] [--no-issue]
"""
from __future__ import annotations

import argparse
import importlib.util
import json
import os
import pathlib
import subprocess
import sys
import urllib.error

HERE = pathlib.Path(__file__).parent
_spec = importlib.util.spec_from_file_location("deploy_preflight", HERE / "deploy_preflight.py")
dp = importlib.util.module_from_spec(_spec)
sys.modules[_spec.name] = dp
_spec.loader.exec_module(dp)

ISSUE_TITLE = "Unit rollback drill failed: an automatic rollback may not work"


def unit_deploy(cmd: str) -> dict:
    out = subprocess.run([str(HERE / "unit_deploy.sh"), cmd], capture_output=True, text=True, timeout=120)
    if out.returncode != 0:
        raise RuntimeError(f"unit_deploy.sh {cmd}: {out.stderr.strip()[-300:]}")
    return json.loads(out.stdout)


def judge(ready: dict, preflight: dict, label_ok: bool) -> list[tuple[str, bool, str]]:
    """(check, passed, detail) for every check."""
    rows = []
    current = ready.get("current") or ""
    rows.append(("active", bool(current), current or "no active release"))
    last = ready.get("history_last") or ""
    rows.append(("history", bool(current) and last == current,
                 f"history ends with {last or 'nothing'}" + ("" if last == current else f", active is {current}")))
    pred = ready.get("predecessor") or ""
    rows.append(("predecessor", bool(pred) and ready.get("predecessor_installed") is True,
                 f"{pred or 'none recorded'}: " + ("installed" if ready.get("predecessor_installed") else "missing")))
    known, known_ok = ready.get("watch_known_good"), ready.get("watch_known_good_installed")
    rows.append(("known-good", known_ok is not False,
                 "no watch record" if known is None else f"{known}: {'installed' if known_ok else 'missing'}"))
    rows.append(("host-config", ready.get("host_config_record") is True,
                 "record present" if ready.get("host_config_record") else "no host-config record"))
    prom = (preflight or {}).get("prometheus") or {}
    rows.append(("prometheus", prom.get("ok") is True, "answers" if prom.get("ok") else prom.get("error", "no answer")))
    rows.append(("freeze", label_ok, f"`{dp.FREEZE_LABEL}` label " + ("exists" if label_ok else "missing")))
    return rows


def label_exists(gh) -> bool:
    try:
        gh.get(f"labels/{dp.FREEZE_LABEL}")
        return True
    except urllib.error.HTTPError as err:
        if err.code == 404:
            return False
        raise


def markdown(rows) -> str:
    lines = ["## Unit rollback drill", "", "| check | result | detail |", "| --- | --- | --- |"]
    lines += [f"| {n} | {'pass' if ok else 'FAIL'} | {d} |" for n, ok, d in rows]
    return "\n".join(lines) + "\n"


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY", ""))
    ap.add_argument("--summary", default=os.environ.get("GITHUB_STEP_SUMMARY", ""))
    ap.add_argument("--no-issue", action="store_true")
    args = ap.parse_args(argv)
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN") or ""
    if not args.repo or not token:
        print("rollback_drill: needs --repo and GH_TOKEN", file=sys.stderr)
        return 2
    gh = dp.GitHub(args.repo, token)
    try:
        ready, preflight = unit_deploy("rollback-readiness"), unit_deploy("preflight-state")
        rows = judge(ready, preflight, label_exists(gh))
    except (RuntimeError, ValueError, OSError, urllib.error.URLError, subprocess.SubprocessError) as err:
        rows = [("control plane", False, f"cannot read: {err}")]
    report = markdown(rows)
    print(report)
    if args.summary:
        with open(args.summary, "a", encoding="utf-8") as fh:
            fh.write(report)
    if all(ok for _, ok, _ in rows):
        return 0
    if not args.no_issue:
        run = f"{os.environ.get('GITHUB_SERVER_URL', 'https://github.com')}/{args.repo}/actions/runs/{os.environ.get('GITHUB_RUN_ID', '')}"
        try:
            issue = gh.post("issues", {"title": ISSUE_TITLE, "body": report + f"\nRun: {run}\n"})
            print(f"::error::rollback drill failed: {issue.get('html_url')}")
        except (OSError, urllib.error.URLError) as err:
            print(f"::error::rollback drill failed and the issue could not be opened: {err}")
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
