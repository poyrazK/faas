#!/usr/bin/env python3
"""Cancel queued and running pull request workflow runs whose PR now conflicts.

GitHub does not start pull_request workflows for a PR that already has a
merge conflict, but a run that started while the PR was clean keeps going
after main moves under it: it tests a merge commit that can no longer land
and holds hosted runners the rest of the queue is waiting for. This scans
the active pull_request runs, asks GitHub for each owning PR's mergeability,
and cancels the runs of PRs in the "dirty" (conflicting) state.

Only PRs that own an active run are queried. GitHub computes mergeability
lazily and answers null until it has; such PRs are retried briefly and then
left alone, so an unknown state never cancels anything.

Requires GH_TOKEN (actions: write, pull-requests: read) and the gh CLI.

Usage:
  scripts/ci/cancel_conflicted_pr_runs.py --repo owner/name [--dry-run]
"""
import argparse
import json
import subprocess
import sys
import time

ACTIVE_STATUSES = ("queued", "in_progress", "waiting", "pending", "requested")


def gh_api(path, method="GET"):
    out = subprocess.run(["gh", "api", "-X", method, path], capture_output=True, text=True)
    if out.returncode != 0:
        raise RuntimeError(f"gh api {method} {path}: {out.stderr.strip() or out.stdout.strip()}")
    return json.loads(out.stdout) if out.stdout.strip() else {}


def active_pr_runs(api, repo):
    runs = {}
    for status in ACTIVE_STATUSES:
        for page in range(1, 6):
            batch = api(f"repos/{repo}/actions/runs?event=pull_request&status={status}&per_page=100&page={page}")
            for run in batch.get("workflow_runs", []):
                runs[run["id"]] = run
            if len(batch.get("workflow_runs", [])) < 100:
                break
    return list(runs.values())


def owning_prs(run, open_prs):
    """PR numbers a run belongs to: the run's own pull_requests list (same-
    repository PRs), else the open PR whose head matches (fork PRs)."""
    numbers = {pr["number"] for pr in run.get("pull_requests") or []}
    if numbers:
        return numbers
    head = (run.get("head_repository") or {}).get("full_name"), run.get("head_branch")
    return {pr["number"] for pr in open_prs
            if ((pr.get("head") or {}).get("repo") or {}).get("full_name") == head[0]
            and (pr.get("head") or {}).get("ref") == head[1]}


def conflicted(api, repo, number, sleep=time.sleep, attempts=4):
    """True only when GitHub reports the PR open and conflicting; None or an
    unknown state after the retries counts as not conflicted."""
    for attempt in range(attempts):
        pr = api(f"repos/{repo}/pulls/{number}")
        if pr.get("state") != "open":
            return False
        if pr.get("mergeable") is not None:
            return pr.get("mergeable") is False and pr.get("mergeable_state") == "dirty"
        if attempt + 1 < attempts:
            sleep(3)
    return False


def open_pulls(api, repo):
    pulls = []
    for page in range(1, 6):
        batch = api(f"repos/{repo}/pulls?state=open&per_page=100&page={page}")
        pulls += batch
        if len(batch) < 100:
            break
    return pulls


def sweep(api, repo, dry_run=False, sleep=time.sleep, log=print):
    """Cancel the active runs of conflicting PRs; return the cancelled run ids."""
    runs = active_pr_runs(api, repo)
    if not runs:
        log("no active pull request runs")
        return []
    needs_lookup = any(not run.get("pull_requests") for run in runs)
    open_prs = open_pulls(api, repo) if needs_lookup else []
    by_pr = {}
    for run in runs:
        for number in owning_prs(run, open_prs):
            by_pr.setdefault(number, []).append(run)
    cancelled = []
    for number in sorted(by_pr):
        if not conflicted(api, repo, number, sleep=sleep):
            continue
        for run in by_pr[number]:
            log(f"PR #{number} conflicts with its base: cancelling {run['name']} run {run['id']} ({run['status']})")
            if not dry_run:
                try:
                    api(f"repos/{repo}/actions/runs/{run['id']}/cancel", method="POST")
                except RuntimeError as err:
                    # A run that finished meanwhile cannot be cancelled.
                    log(f"  not cancelled: {err}")
                    continue
            cancelled.append(run["id"])
    log(f"{len(runs)} active pull request runs across {len(by_pr)} PRs; "
        f"{'would cancel' if dry_run else 'cancelled'} {len(cancelled)}")
    return cancelled


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo", required=True, help="owner/name")
    ap.add_argument("--dry-run", action="store_true", help="report without cancelling")
    args = ap.parse_args()
    sweep(gh_api, args.repo, dry_run=args.dry_run)


if __name__ == "__main__":
    sys.exit(main())
