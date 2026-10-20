#!/usr/bin/env python3
"""Deploy admission for automatic per-unit deploys: wait until production can
take a deploy, and say why it waited
(docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md, section 2).

release-units.yml runs this before the control-plane lane. Every round it
checks:

  freeze   no open issue labelled deploy-freeze             else hold
  newest   no newer run of release-units will deploy        else stand down
  main     the commit's post-merge ci-light run did not fail else fail
  idle     no other deploy runs, the control plane's deploy lock is free
                                                            else wait
  healthy  no page alert fires on the control plane's Prometheus (tenant-
           signal families excepted) and Prometheus answers else wait
  watch    no host already holds the most unwatched releases allowed
           (release_units.json "watch".max_unwatched)      else wait while
           its watch runs; fail when no watch runs for it (adopt it)

A hold or a stand-down ends the job without deploying and without failing:
a person froze deploys, or a newer cumulative unit release takes over.
Waiting ends at --wait; the job then fails and the next merge tries again.

Exit codes: 0 with go=true (deploy) or go=false (hold, stand down) written
to --output; 1 when main's light tier failed or the wait ran out; 2 on a
usage error.

The host checks come from `scripts/ci/unit_deploy.sh preflight-state`, run
over SSH on the control plane (one round-trip a round). The GitHub checks
read the REST API with the workflow token, whose budget is 1,000 requests an
hour per repository, so a round costs about six requests and rounds are a
minute apart.
"""
from __future__ import annotations

import argparse
import importlib.util
import json
import os
import pathlib
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field

HERE = pathlib.Path(__file__).parent

FREEZE_LABEL = "deploy-freeze"
# Runs of these workflows change production: one in flight means wait.
DEPLOY_WORKFLOWS = ("cd-platform.yml", "cd-compute.yml", "cd-controlplane.yml", "cd-edge-units.yml", "cd-edge-node.yml")
UNITS_WORKFLOW = "release-units.yml"
LIGHT_WORKFLOW = "ci-light.yml"
# release-units.yml job names: a run whose lane jobs are queued or running
# deploys now. Reusable workflows' jobs are named "<caller> / <callee>".
LANE_JOB_PREFIXES = ("Deploy the control-plane lane", "Deploy the edge lane")
BUILD_JOB = "Build and sign changed control-plane units"
PREFLIGHT_JOB = "Check production can take a deploy"
ACTIVE_RUN_STATUSES = ("in_progress", "queued", "waiting")
ACTIVE_JOB_STATUSES = ("in_progress", "queued", "waiting", "pending", "requested")


def _tenant_signal_families() -> tuple[str, ...]:
    """The alert families the SLO gate ignores, from slo_gate.py itself."""
    module = sys.modules.get("slo_gate")
    if module is None:
        spec = importlib.util.spec_from_file_location("slo_gate", HERE / "slo_gate.py")
        module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = module  # its dataclasses resolve through sys.modules
        spec.loader.exec_module(module)
    return tuple(module.TENANT_SIGNAL_FAMILIES)


TENANT_SIGNAL_FAMILIES = _tenant_signal_families()
MAX_UNWATCHED = json.loads((HERE / "release_units.json").read_text(encoding="utf-8"))["watch"]["max_unwatched"]

OK, WAIT, HOLD, STAND_DOWN, FAIL = "ok", "wait", "hold", "stand down", "fail"


@dataclass
class Verdict:
    check: str
    state: str
    detail: str


class GitHub:
    """The REST API with a bearer token."""

    def __init__(self, repo: str, token: str, api_url: str = "https://api.github.com", timeout: float = 20.0):
        self.repo, self.token, self.api_url, self.timeout = repo, token, api_url.rstrip("/"), timeout

    def get(self, path: str, params: dict | None = None) -> dict | list:
        url = f"{self.api_url}/repos/{self.repo}/{path.lstrip('/')}"
        if params:
            url += "?" + urllib.parse.urlencode(params)
        return self._send(urllib.request.Request(url, headers=self._headers()))

    def post(self, path: str, body: dict) -> dict:
        url = f"{self.api_url}/repos/{self.repo}/{path.lstrip('/')}"
        headers = {**self._headers(), "Content-Type": "application/json"}
        return self._send(urllib.request.Request(url, data=json.dumps(body).encode(), headers=headers, method="POST"))

    def _headers(self) -> dict:
        return {"Accept": "application/vnd.github+json", "Authorization": f"Bearer {self.token}",
                "X-GitHub-Api-Version": "2022-11-28"}

    def _send(self, req: urllib.request.Request):
        with urllib.request.urlopen(req, timeout=self.timeout) as resp:  # noqa: S310 - fixed API host
            return json.load(resp)


def host_state_command(script: str) -> dict:
    """Run unit_deploy.sh preflight-state; its JSON is the host's view."""
    out = subprocess.run([script, "preflight-state"], check=True, capture_output=True, text=True, timeout=60)
    return json.loads(out.stdout)


@dataclass
class Preflight:
    gh: GitHub
    host_state: object  # () -> dict
    run_id: int
    sha: str
    log: object = print
    light_verdict: Verdict | None = None
    _me: dict | None = None

    def me(self) -> dict:
        if self._me is None:
            self._me = self.gh.get(f"actions/runs/{self.run_id}")
        return self._me

    def evaluate(self) -> list[Verdict]:
        verdicts = [self.freeze(), self.newest(), self.main()]
        try:
            host = self.host_state()
        except (OSError, ValueError, subprocess.SubprocessError) as err:
            host = {"error": str(err)}
        verdicts += [self.idle(host), self.healthy(host), self.watch(host)]
        return verdicts

    # --- freeze -----------------------------------------------------------
    def freeze(self) -> Verdict:
        issues = [i for i in self.gh.get("issues", {"labels": FREEZE_LABEL, "state": "open", "per_page": 20})
                  if "pull_request" not in i]
        if not issues:
            return Verdict("freeze", OK, f"no open `{FREEZE_LABEL}` issue")
        named = "; ".join(f"#{i['number']} {i['title']} ({i['html_url']})" for i in issues)
        return Verdict("freeze", HOLD, f"deploys are frozen by {named}; close the issue to lift it")

    # --- newest -----------------------------------------------------------
    def newest(self) -> Verdict:
        me = self.me()
        # Pushes and dispatches alike: either may carry a deploy.
        runs = self.gh.get(f"actions/workflows/{UNITS_WORKFLOW}/runs",
                           {"branch": "main", "per_page": 20}).get("workflow_runs", [])
        newer = sorted((r for r in runs if r["run_number"] > me["run_number"]), key=lambda r: r["run_number"])
        building = []
        for run in newer:
            jobs = self.jobs(run["id"])
            preflight = next((j for j in jobs if j["name"] == PREFLIGHT_JOB), None)
            if preflight is not None and preflight.get("conclusion") != "skipped":
                return Verdict("newest", STAND_DOWN,
                               f"newer unit release run {run['id']} ({run['head_sha'][:12]}) deploys instead; "
                               "unit releases are cumulative")
            build = next((j for j in jobs if j["name"] == BUILD_JOB), None)
            if run["status"] != "completed" and (build is None or build["status"] != "completed"):
                building.append(run)
        if building:
            names = ", ".join(f"run {r['id']} ({r['head_sha'][:12]})" for r in building)
            return Verdict("newest", WAIT, f"a newer merge's unit release is building ({names}); "
                           "it supersedes this one if it is deployable")
        return Verdict("newest", OK, "this is the newest deployable unit release")

    def jobs(self, run_id: int) -> list[dict]:
        return self.gh.get(f"actions/runs/{run_id}/jobs", {"per_page": 100}).get("jobs", [])

    # --- main -------------------------------------------------------------
    def main(self) -> Verdict:
        if self.light_verdict is not None:
            return self.light_verdict
        runs = self.gh.get(f"actions/workflows/{LIGHT_WORKFLOW}/runs",
                           {"head_sha": self.sha, "event": "push", "per_page": 10}).get("workflow_runs", [])
        failed = [r for r in runs if r.get("status") == "completed" and r.get("conclusion") in ("failure", "timed_out")]
        if failed:
            self.light_verdict = Verdict("main", FAIL, f"main's light tier failed on {self.sha[:12]}: {failed[0]['html_url']}")
            return self.light_verdict
        passed = [r for r in runs if r.get("conclusion") == "success"]
        if passed:
            self.light_verdict = Verdict("main", OK, f"main's light tier passed on {self.sha[:12]}")
            return self.light_verdict
        # Not finished, replaced before it started, or not started: the
        # post-deploy watch reads the verdict when it arrives.
        state = (runs[0].get("conclusion") or runs[0].get("status")) if runs else "not started"
        return Verdict("main", OK, f"main's light tier on {self.sha[:12]} has not failed ({state}); the watch reads its result")

    # --- idle -------------------------------------------------------------
    def idle(self, host: dict) -> Verdict:
        busy = []
        paths = {f".github/workflows/{w}" for w in DEPLOY_WORKFLOWS}
        seen = set()
        for status in ACTIVE_RUN_STATUSES:
            for run in self.gh.get("actions/runs", {"status": status, "per_page": 100}).get("workflow_runs", []):
                if run["id"] == self.run_id or run["id"] in seen:
                    continue
                seen.add(run["id"])
                if run.get("path", "").split("@")[0] in paths:
                    busy.append(f"{run['name']} run {run['id']} is {run['status']} ({run['html_url']})")
                elif run.get("path", "").split("@")[0] == f".github/workflows/{UNITS_WORKFLOW}":
                    lanes = [j["name"] for j in self.jobs(run["id"])
                             if j["name"].startswith(LANE_JOB_PREFIXES) and j["status"] in ACTIVE_JOB_STATUSES]
                    if lanes:
                        busy.append(f"unit release run {run['id']} is deploying ({lanes[0]})")
        if host.get("error"):
            busy.append(f"cannot read the control plane's deploy lock: {host['error']}")
        elif host.get("lock") != "free":
            busy.append(f"the control plane's deploy lock is {host.get('lock', 'unknown')}")
        if busy:
            return Verdict("idle", WAIT, "; ".join(busy))
        return Verdict("idle", OK, "no other deploy is running and the deploy lock is free")

    # --- healthy ----------------------------------------------------------
    def healthy(self, host: dict) -> Verdict:
        if host.get("error"):
            return Verdict("healthy", WAIT, f"cannot read the control plane: {host['error']}")
        prom = host.get("prometheus") or {}
        if not prom.get("ok"):
            return Verdict("healthy", WAIT, f"Prometheus does not answer: {prom.get('error', 'no response')}")
        paging, ignored = [], []
        for alert in prom.get("alerts") or []:
            labels = alert.get("labels") or {}
            if alert.get("state") != "firing" or labels.get("severity") != "page":
                continue
            name = f"{labels.get('alertname', '?')}{{{', '.join(f'{k}={v}' for k, v in sorted(labels.items()) if k in ('component', 'instance', 'job', 'node'))}}}"
            if labels.get("family") in TENANT_SIGNAL_FAMILIES:
                ignored.append(name)
            else:
                paging.append(f"{name} since {alert.get('activeAt', '?')}")
        if paging:
            return Verdict("healthy", WAIT, "page alerts firing: " + "; ".join(paging))
        note = f" (tenant-signal alerts ignored: {', '.join(ignored)})" if ignored else ""
        return Verdict("healthy", OK, "no page alert is firing" + note)


    # --- watch ------------------------------------------------------------
    def watch(self, host: dict) -> Verdict:
        record = host.get("watch")
        if host.get("error") or not record:
            return Verdict("watch", OK, "no unit release awaits its post-deploy watch")
        full = sorted(h for h, e in (record.get("hosts") or {}).items() if len(e.get("releases") or []) >= MAX_UNWATCHED)
        owner = str(record.get("owner_run", ""))
        if not full:
            return Verdict("watch", OK, f"run {owner}'s watch covers the unwatched releases; this deploy joins it")
        run = self.gh.get(f"actions/runs/{owner}") if owner.isdigit() else {}
        where = ", ".join(full)
        if run.get("status") not in (None, "completed"):
            return Verdict("watch", WAIT, f"{where} already hold {MAX_UNWATCHED} unwatched releases; "
                           f"waiting for run {owner}'s post-deploy watch ({run.get('html_url', '')})")
        return Verdict("watch", FAIL, f"{where} hold {MAX_UNWATCHED} unwatched releases and no watch runs for "
                       f"them (run {owner} ended); dispatch cd-unit-watch.yml with adopt=true "
                       "(docs/runbooks/DeployFreeze.md)")


@dataclass
class Report:
    started: float
    # check -> {"first": t, "last": t, "detail": str, "open": bool}: when the
    # check first waited, when it last waited or cleared, and why.
    waited: dict = field(default_factory=dict)

    def note(self, verdicts: list[Verdict], now: float, log) -> None:
        waiting = {v.check: v for v in verdicts if v.state == WAIT}
        for check, entry in self.waited.items():
            if entry["open"] and check not in waiting:
                entry["last"], entry["open"] = now, False
                log(f"deploy preflight: {check} cleared after {round(now - entry['first'])} s")
        for check, v in waiting.items():
            entry = self.waited.get(check)
            if entry is None or not entry["open"] or entry["detail"] != v.detail:
                log(f"::notice::deploy preflight waits: {check}: {v.detail}")
            if entry is None:
                self.waited[check] = {"first": now, "last": now, "detail": v.detail, "open": True}
            else:
                entry.update(last=now, detail=v.detail, open=True)

    def markdown(self, verdicts: list[Verdict], outcome: str, now: float) -> str:
        lines = ["## Deploy preflight", "", outcome, "", "| check | state | detail |", "| --- | --- | --- |"]
        lines += [f"| {v.check} | {v.state} | {v.detail} |" for v in verdicts]
        if self.waited:
            lines += ["", "Waited for:", ""]
            lines += [f"- {check}: {round(e['last'] - e['first'])} s, last: {e['detail']}"
                      for check, e in self.waited.items()]
        lines += ["", f"Total time in preflight: {round(now - self.started)} s."]
        return "\n".join(lines) + "\n"


def run(pre: Preflight, wait: float, interval: float, now=time.time, sleep=time.sleep, log=print) -> tuple[int, bool, str]:
    """Return (exit code, go, summary markdown)."""
    report = Report(started=now())
    while True:
        try:
            verdicts = pre.evaluate()
        except (OSError, ValueError, KeyError, urllib.error.URLError) as err:
            verdicts = [Verdict("github", WAIT, f"GitHub API: {err}")]
        t = now()
        states = {v.state for v in verdicts}
        report.note(verdicts, t, log)
        if FAIL in states:
            failed = next(v for v in verdicts if v.state == FAIL)
            log(f"::error::deploy preflight: {failed.detail}")
            return 1, False, report.markdown(verdicts, f"**Not deployed:** {failed.detail}", t)
        if HOLD in states:
            held = next(v for v in verdicts if v.state == HOLD)
            log(f"::notice::deploy preflight: held: {held.detail}")
            return 0, False, report.markdown(verdicts, f"**Held, not deployed:** {held.detail}", t)
        if STAND_DOWN in states:
            down = next(v for v in verdicts if v.state == STAND_DOWN)
            log(f"::notice::deploy preflight: {down.detail}")
            return 0, False, report.markdown(verdicts, f"**Stood down:** {down.detail}", t)
        if WAIT not in states:
            log("deploy preflight: production can take the deploy")
            return 0, True, report.markdown(verdicts, "**Go.** Production is idle, healthy and not frozen.", t)
        if t - report.started >= wait:
            waiting = "; ".join(f"{v.check}: {v.detail}" for v in verdicts if v.state == WAIT)
            log(f"::error::deploy preflight: still waiting after {round(t - report.started)} s: {waiting}")
            return 1, False, report.markdown(
                verdicts, f"**Not deployed:** waited {round(t - report.started)} s; the next merge tries again.", t)
        sleep(min(interval, max(wait - (t - report.started), 1)))


def parse_duration(text: str) -> float:
    units = {"s": 1, "m": 60, "h": 3600}
    text = text.strip()
    try:
        return float(text[:-1]) * units[text[-1]] if text[-1] in units else float(text)
    except (ValueError, IndexError) as err:
        raise argparse.ArgumentTypeError(f"bad duration {text!r}") from err


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY", ""))
    parser.add_argument("--run-id", type=int, default=int(os.environ.get("GITHUB_RUN_ID", "0") or 0))
    parser.add_argument("--sha", default=os.environ.get("GITHUB_SHA", ""))
    parser.add_argument("--wait", type=parse_duration, default=parse_duration("40m"))
    parser.add_argument("--interval", type=parse_duration, default=parse_duration("60s"))
    parser.add_argument("--host-state", default=str(HERE / "unit_deploy.sh"),
                        help="command whose `preflight-state` prints the control plane's state as JSON")
    parser.add_argument("--summary", default=os.environ.get("GITHUB_STEP_SUMMARY", ""))
    parser.add_argument("--output", default=os.environ.get("GITHUB_OUTPUT", ""))
    args = parser.parse_args(argv)
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN", "")
    if not args.repo or not args.run_id or len(args.sha) != 40 or not token:
        print("::error::deploy preflight needs --repo, --run-id, a 40-hex --sha and GH_TOKEN", file=sys.stderr)
        return 2
    pre = Preflight(GitHub(args.repo, token), lambda: host_state_command(args.host_state), args.run_id, args.sha)
    code, go, summary = run(pre, args.wait, args.interval)
    if args.summary:
        with open(args.summary, "a", encoding="utf-8") as f:
            f.write(summary)
    else:
        print(summary)
    if args.output:
        with open(args.output, "a", encoding="utf-8") as f:
            f.write(f"go={'true' if go else 'false'}\n")
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
