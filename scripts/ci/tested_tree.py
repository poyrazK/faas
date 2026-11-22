#!/usr/bin/env python3
"""Was the tree a unit release ships the tree CI tested? If not, test it.

docs/adr/20261122170000-continuous-unit-lane.md, "Tested as shipped". The
repository has no merge queue, so a squash merge can land a tree no CI run
saw: two PRs that pass alone, merged into each other. Before release-units
signs a release of commit T:

  tested   T's PR (the pull request T was merged from) had main's previous
           commit T^ in its final head H, H's tree is T's tree, and ci-light
           passed on H. CI's merge ref for H was then H itself, so CI tested
           exactly what ships. Nothing more to run.
  run      otherwise. scripts/ci/affected_packages.py selects packages as
           ci-light would, and only the ones a shipped daemon builds from
           are kept (`go list -deps` of their cmd/ packages). The caller
           runs their tests at T and releases only when they pass.
           When ci-light passed on H (main moved under the pull request):
           H tested T's own change (T^..T) over an older main, and main
           tested the other merges (H..T). A package affected by only one
           side has inputs CI already saw together, so only packages
           affected by both (`interaction`) can fail at T alone. Without a
           green H: every package T's own change or the other merges affect.

Without a pull request (none found, or the API failed) it falls back to
`run` over T^..T.

Usage:
  scripts/ci/tested_tree.py --commit SHA --daemons apid,meterd [--repo owner/name] [--output plan.json]

Prints {"verdict": "tested"|"run", "reason", "pull", "head", "paths",
"packages"}; packages are import paths (empty for tested, and for run when
no shipped daemon builds from the affected packages).
"""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
MODULE = "github.com/onebox-faas/faas"
LIGHT_WORKFLOW = "ci-light.yml"


def git(*args: str, check: bool = True) -> str:
    out = subprocess.run(["git", *args], capture_output=True, text=True)
    if check and out.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)}: {out.stderr.strip()}")
    return out.stdout


def is_ancestor(a: str, b: str) -> bool:
    return subprocess.run(["git", "merge-base", "--is-ancestor", a, b], capture_output=True).returncode == 0


class GitHub:
    def __init__(self, repo: str, token: str):
        self.repo, self.token = repo, token

    def get(self, path: str, params: dict | None = None):
        query = "&".join(f"{k}={v}" for k, v in (params or {}).items())
        req = urllib.request.Request(f"https://api.github.com/repos/{self.repo}/{path}" + (f"?{query}" if query else ""),
                                     headers={"Authorization": f"Bearer {self.token}",
                                              "Accept": "application/vnd.github+json",
                                              "X-GitHub-Api-Version": "2022-11-28"})
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.load(resp)


def merged_pull(gh, commit: str) -> dict | None:
    """The merged pull request whose merge commit is `commit`."""
    for pull in gh.get(f"commits/{commit}/pulls"):
        if pull.get("merge_commit_sha") == commit and pull.get("merged_at"):
            return pull
    return None


def light_passed(gh, head: str) -> bool:
    runs = gh.get(f"actions/workflows/{LIGHT_WORKFLOW}/runs",
                  {"head_sha": head, "event": "pull_request", "per_page": 20}).get("workflow_runs", [])
    return any(r.get("conclusion") == "success" for r in runs)


def decide(commit: str, pull: dict | None, light_ok, fetch_head, tree, ancestor, diff) -> dict:
    """The verdict for `commit` (pure apart from the callables, for tests):
    light_ok(head) whether ci-light passed on head; fetch_head(pull) makes
    the pull's head available locally and returns its sha (or None);
    tree(sha) a tree id; ancestor(a, b); diff(a, b) changed paths."""
    parent = f"{commit}^"
    own = diff(parent, commit)
    if pull is None:
        return {"verdict": "run", "reason": "no merged pull request for this commit: testing its own change",
                "pull": None, "head": None, "paths": own}
    head = fetch_head(pull)
    if not head:
        return {"verdict": "run", "reason": f"cannot fetch the head of #{pull['number']}: testing the commit's own change",
                "pull": pull["number"], "head": None, "paths": own}
    up_to_date = ancestor(parent, head)
    same_tree = tree(head) == tree(commit)
    light = light_ok(head)
    if up_to_date and same_tree and light:
        return {"verdict": "tested", "reason": f"ci-light passed on #{pull['number']}'s head {head[:12]}, which held "
                f"main's previous commit and is the tree that shipped", "pull": pull["number"], "head": head, "paths": []}
    others = diff(head, commit)
    why = ("its head did not hold main's previous commit" if not up_to_date else
           "its head's tree differs from the merged tree" if not same_tree else "ci-light did not pass on its head")
    plan = {"verdict": "run", "pull": pull["number"], "head": head, "paths": sorted(set(own) | set(others))}
    if light:
        plan.update(reason=f"#{pull['number']}: {why}; ci-light passed on its head, so testing the packages both "
                    "its change and the merges since affect", interaction={"own": own, "others": others})
    else:
        plan["reason"] = f"#{pull['number']}: {why}; testing what CI did not see together"
    return plan


SHARDS = 3  # ci-light's test shards; every shard's selection is taken


def affected(paths: list[str], kind: str) -> list[str]:
    """ci-light's selection over every shard: `test` (packages run in full)
    or `selective` (large packages whose related tests run)."""
    if not paths:
        return []
    found: set[str] = set()
    for shard in range(1, SHARDS + 1):
        out = subprocess.run([sys.executable, os.path.join(HERE, "affected_packages.py"), kind,
                              "--shard", str(shard), "--shards", str(SHARDS)],
                             input="\n".join(paths) + "\n", capture_output=True, text=True, check=True)
        found.update(out.stdout.split())
    return sorted(found)


def select(plan: dict, kind: str, affected=affected) -> list[str]:
    """The packages of `kind` a run plan tests: with `interaction`, those
    both sides affect; otherwise those any of its paths affect."""
    both = plan.get("interaction")
    if both:
        return sorted(set(affected(both["own"], kind)) & set(affected(both["others"], kind)))
    return affected(plan["paths"], kind)


def related_runs(package: str, paths: list[str], base: str) -> list[str]:
    """-run patterns of the large package's tests related to the change, as
    ci-light selects them (select_package_tests.py)."""
    out = subprocess.run([sys.executable, os.path.join(HERE, "select_package_tests.py"), "--package",
                          package.removeprefix("./"), "--cap", "150", "--batch-size", "50", "--base", base],
                         input="\n".join(paths) + "\n", capture_output=True, text=True, check=True)
    return [line for line in out.stdout.splitlines() if line.strip()]


def shipped_closure(daemons: list[str]) -> set[str]:
    """Import paths every shipped daemon builds from (linux/amd64, CGO off)."""
    if not daemons:
        return set()
    env = dict(os.environ, GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
    out = subprocess.run(["go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}",
                          *[f"./cmd/{d}" for d in daemons]], capture_output=True, text=True, env=env, check=True)
    return {line.strip() for line in out.stdout.splitlines() if line.strip()}


def normalise(pkg: str) -> str:
    pkg = pkg.strip()
    if pkg.startswith("./"):
        return f"{MODULE}/{pkg[2:]}".rstrip("/")
    return pkg


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--commit", required=True)
    ap.add_argument("--daemons", default="", help="comma-separated shipped daemons (cmd/ names)")
    ap.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY", ""))
    ap.add_argument("--output")
    args = ap.parse_args(argv)
    commit = git("rev-parse", "--verify", f"{args.commit}^{{commit}}").strip()
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN") or ""
    gh = GitHub(args.repo, token) if args.repo and token else None
    pull = None
    if gh:
        try:
            pull = merged_pull(gh, commit)
        except (OSError, ValueError, urllib.error.URLError) as err:
            print(f"warning: cannot read the pull request of {commit[:12]}: {err}", file=sys.stderr)

    def fetch_head(p: dict) -> str | None:
        try:
            git("fetch", "--quiet", "--filter=blob:none", "origin", f"+refs/pull/{p['number']}/head:refs/tested-tree/head")
            return git("rev-parse", "refs/tested-tree/head").strip()
        except RuntimeError as err:
            print(f"warning: {err}", file=sys.stderr)
            return None

    def light_ok(head: str) -> bool:
        try:
            return bool(gh) and light_passed(gh, head)
        except (OSError, ValueError, urllib.error.URLError) as err:
            print(f"warning: cannot read ci-light runs for {head[:12]}: {err}", file=sys.stderr)
            return False

    plan = decide(commit, pull, light_ok, fetch_head,
                  tree=lambda sha: git("rev-parse", f"{sha}^{{tree}}").strip(),
                  ancestor=is_ancestor,
                  diff=lambda a, b: [p for p in git("diff", "--name-only", "--no-renames", a, b).splitlines() if p])
    packages: list[str] = []
    selective: list[dict] = []
    if plan["verdict"] == "run":
        closure = shipped_closure([d for d in args.daemons.split(",") if d])
        packages = sorted(p for p in select(plan, "test") if normalise(p) in closure)
        for pkg in select(plan, "selective"):
            if normalise(pkg) in closure:
                runs = related_runs(pkg, plan["paths"], f"{commit}^")
                if runs:
                    selective.append({"package": pkg, "runs": runs})
    plan["packages"] = packages
    plan["selective"] = selective
    body = json.dumps(plan, indent=2, sort_keys=True) + "\n"
    if args.output:
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(body)
    else:
        sys.stdout.write(body)
    print(f"tested_tree: {plan['verdict']}: {plan['reason']}; {len(packages)} packages and {len(selective)} large packages to test", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
