#!/usr/bin/env python3
"""Deploy admission for automatic per-unit deploys: wait until production can
take a deploy, and say why it waited
(docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md, section 2).

release-units.yml runs this before the control-plane lane. Every round it
checks:

  freeze   no open issue labelled deploy-freeze             else hold
  newest   no newer run of release-units will deploy        else stand down
  current  no earlier run deployed this commit through every lane else stand down
  main     the commit's post-merge ci-light run did not fail else fail
  idle     no other deploy runs, the control plane's deploy lock is free
                                                            else wait
  healthy  no page alert fires on the control plane's Prometheus (tenant-
           signal families excepted) and Prometheus answers else wait
  base     the control plane runs the full release this unit release is
           built over (the build's BASE_MARKER artifact)  else stand down:
           a newer tag is not deployed yet; cd-platform deploys it
  watch    no host already holds the most unwatched releases allowed
           (release_units.json "watch".max_unwatched)      else wait while
           its watch runs; fail when no watch runs for it (adopt it)

With --follow-build (release-units.yml, ADR-20261122170000 "Preflight
beside the build") the job starts with the build instead of after it, and
one more check leads:

  built    this run's build job finished and released a commit (its
           RELEASE_MARKER artifact names it)            else wait while it
           builds; stand down when it failed or released nothing

While only the build is outstanding, the build job is polled every --poll
and the other checks are reread every --interval; the round that sees the
build finish rereads every check, so a go is never older than one round.
The commit the current and main checks read comes from the marker.

A hold or a stand-down ends the job without deploying and without failing:
a person froze deploys, or a newer cumulative unit release takes over.
Waiting ends at --wait; the job then fails and the next merge tries again.

Exit codes: 0 with go=true (deploy) or go=false (hold, stand down) written
to --output; 1 when main's light tier failed or the wait ran out; 2 on a
usage error.

The host checks come from `scripts/ci/unit_deploy.sh preflight-state`, run
over SSH on the control plane (one round-trip a round). The GitHub checks
read the REST API with the workflow token, whose budget is 1,000 requests an
hour per repository, so a round costs about six requests (more pages when
more than 100 runs are active; six more on a round whose every check
passed, to confirm idle) and rounds are a
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
# release-units' build job uploads an empty artifact named this plus the
# released commit, so a later run can find the runs that released it.
RELEASE_MARKER = "unit-release-sha-"
# ... and one named after the full release it is built over (--follow-build).
BASE_MARKER = "unit-release-base-"


def lanes_passed(jobs: list[dict]) -> bool:
    """Whether a release-units run finished every lane it ran (control
    plane, edge) green: at least one ran, none was skipped-over by failure."""
    lanes = [j for j in jobs if j["name"].startswith(LANE_JOB_PREFIXES) and j.get("conclusion") != "skipped"]
    return bool(lanes) and all(j.get("status") == "completed" and j.get("conclusion") == "success" for j in lanes)
ACTIVE_RUN_STATUSES = ("in_progress", "queued", "waiting")
# Pages of 100 the repository-wide active-run listing follows per status.
MAX_ACTIVE_PAGES = 3
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


def light_runs(gh, sha: str) -> list[dict]:
    """main's post-merge light tier runs on one commit."""
    return gh.get(f"actions/workflows/{LIGHT_WORKFLOW}/runs",
                  {"head_sha": sha, "event": "push", "per_page": 10}).get("workflow_runs", [])


def light_run_failed(run: dict) -> bool:
    return run.get("status") == "completed" and run.get("conclusion") in ("failure", "timed_out")


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
    # --follow-build: wait for this run's build and read the released commit
    # from its marker; reread the other checks every full_every seconds.
    follow_build: bool = False
    full_every: float = 60.0
    clock: object = time.time
    _round: list | None = None
    _round_at: float = 0.0
    # The full release this run's unit release is built over (BASE_MARKER),
    # known once the build released something; "" skips the base check.
    base_sha: str = ""

    def me(self) -> dict:
        if self._me is None:
            self._me = self.gh.get(f"actions/runs/{self.run_id}")
        return self._me

    def evaluate(self) -> list[Verdict]:
        lead = []
        if self.follow_build:
            built = self.built()
            if built.state == WAIT and self._round is not None and self.clock() - self._round_at < self.full_every:
                return [built] + self._round
            lead = [built]
        verdicts = [self.freeze(), self.newest()]
        if self.sha:
            verdicts += [self.current(), self.main()]
        try:
            host = self.host_state()
        except (OSError, ValueError, subprocess.SubprocessError) as err:
            host = {"error": str(err)}
        verdicts += [self.idle(host), self.healthy(host), self.base(host), self.watch(host)]
        if all(v.state == OK for v in lead + verdicts):
            # The round would go: confirm idle from each deploy workflow's
            # own runs first (confirm_idle), and only then, to keep a waiting
            # round within the token budget.
            verdicts = [self.confirm_idle() if v.check == "idle" else v for v in verdicts]
        self._round, self._round_at = verdicts, self.clock()
        return lead + verdicts

    # --- built ------------------------------------------------------------
    def built(self) -> Verdict:
        """This run's build job (--follow-build). Its marker artifact names
        the commit it released; upload-artifact v4 lists an artifact as soon
        as it is uploaded."""
        build = next((j for j in self.jobs(self.run_id) if j["name"] == BUILD_JOB), None)
        if build is None or build.get("status") != "completed":
            state = build.get("status", "queued") if build else "not started"
            return Verdict("built", WAIT, f"this run's unit release is building ({state})")
        if build.get("conclusion") != "success":
            return Verdict("built", STAND_DOWN, f"this run's build ended {build.get('conclusion')}; nothing to deploy")
        sha = self.marker(self.run_id)
        if not sha:
            return Verdict("built", STAND_DOWN, "this run's build released nothing; nothing to deploy")
        self.sha = sha
        self.base_sha = self.marker(self.run_id, BASE_MARKER)
        return Verdict("built", OK, f"this run released {sha[:12]}")

    def marker(self, run_id: int, prefix: str = RELEASE_MARKER) -> str:
        """The commit a marker artifact of run_id names: RELEASE_MARKER the
        released commit, BASE_MARKER the full release it is built over."""
        arts = self.gh.get(f"actions/runs/{run_id}/artifacts", {"per_page": 100}).get("artifacts", [])
        names = [a.get("name", "") for a in arts if a.get("name", "").startswith(prefix)]
        return names[0][len(prefix):] if names else ""

    # --- base -------------------------------------------------------------
    def base(self, host: dict) -> Verdict:
        """The planner refuses a unit release built over another full
        release than the control plane runs (N±1). The build takes the
        newest tag, so between a tag and its cd-platform deploy every
        release would fail the lane and freeze deploys; stand down instead.
        Unknown on either side (no marker, an old host script, no release
        manifest): the planner still decides."""
        running = (host.get("base") or "") if not host.get("error") else ""
        if not self.base_sha or not running:
            return Verdict("base", OK, "the planner checks the full release the control plane runs")
        if running == self.base_sha:
            return Verdict("base", OK, f"the control plane runs the full release this unit release is built over ({running[:12]})")
        return Verdict("base", STAND_DOWN, f"the control plane runs full release {running[:12]}, but this unit release is "
                       f"built over {self.base_sha[:12]} (the newest tag): deploy that release with cd-platform first; "
                       "the next merge after it deploys")

    # --- current ----------------------------------------------------------
    def current(self) -> Verdict:
        """A stalled lane (docs/adr/20261122170000-continuous-unit-lane.md)
        releases the same commit on every merge until the next full
        release. Every release-units run that releases a commit uploads the
        marker artifact RELEASE_MARKER + sha; when an earlier such run
        finished every lane it ran (control plane, edge) green, this one has
        nothing to add. A failed lane is retried (once its deploy-freeze
        issue is closed); a release the watch rolled back is not re-shipped
        on its own: a person decides that (ADR 20261020214632)."""
        arts = self.gh.get("actions/artifacts", {"name": RELEASE_MARKER + self.sha, "per_page": 100})
        for run_id in sorted({a.get("workflow_run", {}).get("id") for a in arts.get("artifacts", [])} - {None, self.run_id}):
            if lanes_passed(self.jobs(run_id)):
                return Verdict("current", STAND_DOWN, f"run {run_id} already deployed the unit release of "
                               f"{self.sha[:12]}: every lane it ran passed")
        return Verdict("current", OK, f"no earlier run deployed {self.sha[:12]} through every lane")

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
            build = next((j for j in jobs if j["name"] == BUILD_JOB), None)
            if preflight is not None and preflight.get("conclusion") == "skipped":
                continue  # a build-only run deploys nothing
            built = build is not None and build["status"] == "completed"
            # Its preflight starts with its build (--follow-build), so a
            # running preflight alone is no takeover: the newer run takes
            # over once its build released a commit.
            if preflight is not None and built and build.get("conclusion") == "success":
                if self.marker(run["id"]):
                    return Verdict("newest", STAND_DOWN,
                                   f"newer unit release run {run['id']} ({run['head_sha'][:12]}) deploys instead; "
                                   "unit releases are cumulative")
                continue  # it released nothing
            if run["status"] != "completed" and not built:
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
        runs = light_runs(self.gh, self.sha)
        failed = [r for r in runs if light_run_failed(r)]
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
    def busy_runs(self, runs) -> list[str]:
        """Why the runs keep production busy: a deploy workflow's run, or a
        unit release whose lane jobs are queued or running."""
        busy = []
        paths = {f".github/workflows/{w}" for w in DEPLOY_WORKFLOWS}
        for run in runs:
            if run["id"] == self.run_id:
                continue
            path = run.get("path", "").split("@")[0]
            if path in paths:
                busy.append(f"{run['name']} run {run['id']} is {run['status']} ({run['html_url']})")
            elif path == f".github/workflows/{UNITS_WORKFLOW}":
                lanes = [j["name"] for j in self.jobs(run["id"])
                         if j["name"].startswith(LANE_JOB_PREFIXES) and j["status"] in ACTIVE_JOB_STATUSES]
                if lanes:
                    busy.append(f"unit release run {run['id']} is deploying ({lanes[0]})")
        return busy

    def listed_active(self) -> dict:
        """{run id: run} for every queued, waiting or in-progress run the
        repository-wide listing names, following its pages (a busy
        repository has more than 100 such runs; up to MAX_ACTIVE_PAGES)."""
        found = {}
        for status in ACTIVE_RUN_STATUSES:
            for page in range(1, MAX_ACTIVE_PAGES + 1):
                body = self.gh.get("actions/runs", {"status": status, "per_page": 100, "page": page})
                runs = body.get("workflow_runs", [])
                for run in runs:
                    found.setdefault(run["id"], run)
                if len(runs) < 100 or page * 100 >= int(body.get("total_count") or 0):
                    break
        self._listed = found
        return found

    def idle(self, host: dict) -> Verdict:
        found = self.listed_active()
        busy = self.busy_runs(found[i] for i in sorted(found))
        if host.get("error"):
            busy.append(f"cannot read the control plane's deploy lock: {host['error']}")
        elif host.get("lock") != "free":
            busy.append(f"the control plane's deploy lock is {host.get('lock', 'unknown')}")
        if busy:
            return Verdict("idle", WAIT, "; ".join(busy))
        return Verdict("idle", OK, "no other deploy is running and the deploy lock is free")

    def confirm_idle(self) -> Verdict:
        """idle, confirmed from each deploy workflow's own newest runs, whose
        status is the run's. The repository-wide listing filtered by status
        missed an in-progress cd-platform run on 2026-11-25 (run
        45639008753, rc.381): the lane activated during that full release
        and a canary 503 froze deploys (#8070). evaluate() calls this only
        on a round whose every other check passed."""
        found = getattr(self, "_listed", {})
        confirm = []
        for workflow in DEPLOY_WORKFLOWS + (UNITS_WORKFLOW,):
            # release-units starts a run per merge and every ten minutes, so
            # read further back for it than for the manual deploy workflows.
            per_page = 30 if workflow == UNITS_WORKFLOW else 10
            for run in self.gh.get(f"actions/workflows/{workflow}/runs", {"per_page": per_page}).get("workflow_runs", []):
                if run["id"] not in found and run.get("status") in ACTIVE_RUN_STATUSES:
                    confirm.append(run)
        busy = self.busy_runs(confirm)
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


def run(pre: Preflight, wait: float, interval: float, now=time.time, sleep=time.sleep, log=print,
        poll: float | None = None) -> tuple[int, bool, str]:
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
        # Only the build outstanding (--follow-build): poll it more often,
        # so the deploy starts soon after the build ends.
        building = poll is not None and all(v.check == "built" for v in verdicts if v.state == WAIT)
        sleep(min(poll if building else interval, max(wait - (t - report.started), 1)))


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
    parser.add_argument("--sha", default="",
                        help="the commit this run releases; with --follow-build, read from the build's marker")
    parser.add_argument("--follow-build", action="store_true",
                        help="start beside this run's build job and wait for it (release-units.yml)")
    parser.add_argument("--poll", type=parse_duration, default=parse_duration("10s"),
                        help="with --follow-build, how often the build job is read")
    parser.add_argument("--wait", type=parse_duration, default=parse_duration("40m"))
    parser.add_argument("--interval", type=parse_duration, default=parse_duration("60s"))
    parser.add_argument("--host-state", default=str(HERE / "unit_deploy.sh"),
                        help="command whose `preflight-state` prints the control plane's state as JSON")
    parser.add_argument("--summary", default=os.environ.get("GITHUB_STEP_SUMMARY", ""))
    parser.add_argument("--output", default=os.environ.get("GITHUB_OUTPUT", ""))
    args = parser.parse_args(argv)
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN", "")
    if not args.repo or not args.run_id or not token or (len(args.sha) != 40 and not args.follow_build):
        print("::error::deploy preflight needs --repo, --run-id, GH_TOKEN and a 40-hex --sha or --follow-build",
              file=sys.stderr)
        return 2
    pre = Preflight(GitHub(args.repo, token), lambda: host_state_command(args.host_state), args.run_id, args.sha,
                    follow_build=args.follow_build, full_every=args.interval)
    code, go, summary = run(pre, args.wait, args.interval, poll=args.poll if args.follow_build else None)
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
