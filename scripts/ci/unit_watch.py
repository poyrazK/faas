#!/usr/bin/env python3
"""Post-deploy watch for per-unit releases, the runner side
(docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md, section 3).

After a unit release run's lanes finish, this keeps the SLO gate running
(`unit_deploy.sh watch`, slo_gate.py in watch mode on the control plane) for
30 minutes after the run's last activation, over every unit the control
plane's watch record holds, on every host. Meanwhile it reads, once a
minute, the post-merge light run of each commit the record holds.

  pass        the record is closed: every host's active release is now its
              known-good one
  superseded  a newer run took the record (it deploys and watches both), or a
              person or a full release started deploying; the record stays
  breach      an SLO breached, or a commit's post-merge light run failed: every
              host in the record, newest change first, goes back to its
              known-good release (deployctl rollback --only ... --expect-current
              ... --to ...), the record is closed, and a deploy-freeze issue
              with this report pauses automatic deploys
  inconclusive Prometheus was blind too long: nothing is promoted or rolled
              back; the record stays for the next watch

A lane of the calling run that failed (--deploy-results) also opens the
deploy-freeze issue, before the watch: a failed lane rolls its host back,
and the next merge's cumulative unit release would ship the same change.

Exit 0 for pass, superseded or nothing to watch; 1 for a breach, an
inconclusive watch or a failed lane.

The control plane's SSH settings come from UNIT_SSH_* / UNIT_GATE_* (as for
unit_deploy.sh); compute nodes use --compute-key and the host-key fingerprint
the record keeps for each.
"""
from __future__ import annotations

import argparse
import importlib.util
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
from dataclasses import dataclass, field

HERE = pathlib.Path(__file__).parent
PASS, BREACH, INCONCLUSIVE, SUPERSEDED = 0, 1, 3, 4
NODE_STATE_CHANGED = 5


def _load(name: str):
    module = sys.modules.get(name)
    if module is None:
        spec = importlib.util.spec_from_file_location(name, HERE / f"{name}.py")
        module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
    return module


dp = _load("deploy_preflight")
freeze = _load("deploy_freeze")


@dataclass
class Outcome:
    code: int  # PASS or BREACH (an inconclusive watch also fails the run)
    report: str
    rolled_back: bool = False


@dataclass
class Shell:
    """Runs unit_deploy.sh with one host's SSH settings."""
    script: str
    env: dict
    compute_key: str
    workdir: str
    run: object = subprocess.run
    popen: object = subprocess.Popen

    def ud(self, args: list[str], env: dict | None = None, timeout: float = 600) -> tuple[int, str, str]:
        out = self.run([self.script, *args], env=env or self.env, capture_output=True, text=True, timeout=timeout)
        return out.returncode, out.stdout, out.stderr

    def start(self, args: list[str]):
        return self.popen([self.script, *args], env=self.env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)

    def node_env(self, target: dict) -> dict:
        """SSH settings for a compute node: only its pinned host key."""
        known = os.path.join(self.workdir, f"known-hosts-{target['node']}")
        code, _, err = self.ud(["pin-node", target["ssh_host"], str(target["ssh_port"]),
                                target["ssh_host_key_sha256"], known])
        if code != 0:
            raise RuntimeError(f"cannot verify {target['node']}'s host key: {err.strip()}")
        return {**self.env, "UNIT_SSH_TARGET": f"{target['ssh_user']}@{target['ssh_host']}",
                "UNIT_SSH_PORT": str(target["ssh_port"]), "UNIT_SSH_KEY": self.compute_key,
                "UNIT_SSH_KNOWN_HOSTS": known}


@dataclass
class Watch:
    shell: Shell
    gh: object
    owner: str
    deployctl: str
    run_url: str
    poll: float = 60.0
    now: object = time.time
    sleep: object = time.sleep
    log: object = print
    lines: list = field(default_factory=list)
    light_final: dict = field(default_factory=dict)

    # --- record -----------------------------------------------------------
    def record(self) -> dict | None:
        code, out, err = self.shell.ud(["watch-record"])
        if code != 0:
            raise RuntimeError(f"cannot read the watch record: {err.strip()}")
        return json.loads(out or "null")

    # --- GitHub signals ---------------------------------------------------
    def light_failure(self, commits: list[str]) -> str | None:
        """The first commit whose post-merge light run failed, with its URL."""
        for sha in commits:
            if sha in self.light_final:
                if self.light_final[sha]:
                    return self.light_final[sha]
                continue
            runs = self.gh.get(f"actions/workflows/{dp.LIGHT_WORKFLOW}/runs",
                               {"head_sha": sha, "event": "push", "per_page": 10}).get("workflow_runs", [])
            failed = [r for r in runs if r.get("status") == "completed" and r.get("conclusion") in ("failure", "timed_out")]
            if failed:
                self.light_final[sha] = f"main's light tier failed on {sha[:12]}: {failed[0]['html_url']}"
                return self.light_final[sha]
            if any(r.get("conclusion") == "success" for r in runs):
                self.light_final[sha] = ""
        return None

    def deployer(self) -> str | None:
        """A deploy run (a full release, or a person's dispatch) in flight."""
        paths = {f".github/workflows/{w}" for w in dp.DEPLOY_WORKFLOWS}
        for status in ("in_progress", "queued"):
            for run in self.gh.get("actions/runs", {"status": status, "per_page": 100}).get("workflow_runs", []):
                if run.get("path", "").split("@")[0] in paths:
                    return f"{run['name']} run {run['id']} ({run['html_url']})"
        return None

    # --- the watch --------------------------------------------------------
    def follow(self, record: dict) -> tuple[int, str]:
        """Run the remote watch to its end; return (outcome, why)."""
        commits = [c for h in record["hosts"].values() for c in h.get("commits", [])]
        proc = self.shell.start(["watch"])
        reader = threading.Thread(target=self._read, args=(proc,), daemon=True)
        reader.start()
        stop: tuple[int, str] | None = None
        while proc.poll() is None:
            self.sleep(self.poll)
            if proc.poll() is not None:
                break
            try:
                failed = self.light_failure(commits)
                deployer = None if failed else self.deployer()
            except (OSError, ValueError, KeyError, urllib.error.URLError) as err:
                self.log(f"unit-watch: GitHub API: {err}")
                continue
            if failed:
                stop = (BREACH, failed)
            elif deployer:
                stop = (SUPERSEDED, f"{deployer} is deploying; the watch stands down and the record stays")
            if stop:
                proc.terminate()
                break
        proc.wait()
        reader.join(timeout=5)
        if stop:
            return stop
        code = proc.returncode
        if code == PASS:
            return PASS, "no SLO breached and no post-merge light run failed"
        if code == BREACH:
            return BREACH, "an SLO breached: " + "; ".join(l for l in self.lines if "::error::slo-gate" in l)
        if code == SUPERSEDED:
            return SUPERSEDED, "another run took the watch over: " + "; ".join(l for l in self.lines if "stopping" in l or "does not own" in l)
        return INCONCLUSIVE, f"the watch had no evidence (exit {code})"

    def _read(self, proc) -> None:
        for line in proc.stdout:
            line = line.rstrip("\n")
            self.lines.append(line)
            self.log(line)

    def rollback(self, record: dict) -> list[tuple[str, str]]:
        """Put every host in the record back on its known-good release,
        newest change first. Returns (host, result) pairs."""
        results = []
        for host in reversed(record["order"]):
            entry = record["hosts"][host]
            try:
                env = None if entry.get("target") is None else self.shell.node_env(entry["target"])
            except RuntimeError as err:
                results.append((host, f"not rolled back: {err}"))
                continue
            entry_file = os.path.join(self.shell.workdir, f"entry-{host}.json")
            with open(entry_file, "w", encoding="utf-8") as f:
                json.dump(entry, f)
            code, out, err = self.shell.ud(["watch-stage", self.deployctl], env)
            if code != 0:
                results.append((host, f"not rolled back: staging deployctl failed: {err.strip()}"))
                continue
            stage = out.strip()
            code, out, err = self.shell.ud(["watch-rollback", entry_file, stage], env)
            self.shell.ud(["cleanup", stage], env)
            if code == 0:
                results.append((host, f"on known-good {entry['known_good'][:12]} "
                                      f"(was {entry['current'][:12]}; restarted {', '.join(entry['services'])})"))
            elif code == NODE_STATE_CHANGED:
                results.append((host, f"left alone: {(err or out).strip()}"))
            else:
                results.append((host, f"ROLLBACK FAILED (exit {code}): {(err or out).strip()[-400:]}"))
        return results

    def run(self, adopt: bool = False) -> Outcome:
        if adopt:
            code, _, err = self.shell.ud(["watch-adopt"])
            if code != 0:
                raise RuntimeError(f"cannot adopt the watch record: {err.strip()}")
        record = self.record()
        title = "## Post-deploy watch\n\n"
        if record is None:
            return Outcome(PASS, title + "Nothing to watch: every unit release is watched clean.\n")
        if record.get("owner_run") != self.owner:
            return Outcome(PASS, title + f"Run {record.get('owner_run')} took the watch over before this one "
                                         "started; it watches these releases.\n")
        started = self.now()
        outcome, why = self.follow(record)
        minutes = round((self.now() - started) / 60)
        hosts = self.describe(record)
        if outcome == PASS:
            code, _, err = self.shell.ud(["watch-close"])
            if code == SUPERSEDED:
                return Outcome(PASS, title + f"**Superseded** at the end: {err.strip()}\n\n{hosts}")
            if code != 0:
                return Outcome(BREACH, title + f"**Passed**, but closing the record failed: {err.strip()}\n\n{hosts}")
            return Outcome(PASS, title + f"**Passed** after {minutes} min: {why}. Now known-good:\n\n{hosts}")
        if outcome == SUPERSEDED:
            return Outcome(PASS, title + f"**Superseded** after {minutes} min: {why}\n\n{hosts}")
        if outcome == INCONCLUSIVE:
            return Outcome(BREACH, title + f"**Inconclusive** after {minutes} min: {why}. Nothing was promoted or "
                                           "rolled back; the record stays for the next watch.\n\n" + hosts)
        # Breach: roll back what the record holds, if this run still owns it.
        record = self.record()
        if record is None or record.get("owner_run") != self.owner:
            return Outcome(BREACH, title + f"**Breach** after {minutes} min, but run "
                                           f"{(record or {}).get('owner_run')} took the watch over before the "
                                           f"rollback; its watch judges the same releases.\n\n{why}\n")
        results = self.rollback(record)
        code, _, err = self.shell.ud(["watch-close"])
        closed = "" if code == 0 else f"\n\nClosing the watch record failed: {err.strip()}"
        report = ("## Post-deploy watch: automatic rollback\n\n"
                  f"**Breach** after {minutes} min: {why}\n\nRolled back, newest change first:\n\n"
                  + "\n".join(f"- {host}: {result}" for host, result in results)
                  + f"\n\nWhat the record held:\n\n{hosts}\nRun: {self.run_url}{closed}\n")
        return Outcome(BREACH, report, rolled_back=True)

    @staticmethod
    def describe(record: dict) -> str:
        lines = []
        for host in record["order"]:
            e = record["hosts"][host]
            lines.append(f"- {host}: known-good `{e['known_good'][:12]}`, active `{e['current'][:12]}`, "
                         f"{len(e['releases'])} unwatched release(s), services {', '.join(e['services'])}, "
                         f"commits {', '.join(c[:12] for c in e.get('commits', [])) or '-'}")
        return "\n".join(lines) + "\n"


def failed_lanes(results: str) -> list[str]:
    """The jobs of `job=result,...` whose result is failure."""
    pairs = [p.split("=", 1) for p in results.split(",") if "=" in p]
    return [job.strip() for job, result in pairs if result.strip() == "failure"]


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--deployctl", required=True, help="the signed unit release's tools/deployctl")
    parser.add_argument("--compute-key", default="", help="SSH key for compute nodes")
    parser.add_argument("--adopt", action="store_true", help="take over a record no watch runs for")
    parser.add_argument("--deploy-results", default="", help="the calling run's lanes as job=result pairs")
    parser.add_argument("--summary", default=os.environ.get("GITHUB_STEP_SUMMARY", ""))
    args = parser.parse_args(argv)
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN", "")
    repo, owner = os.environ.get("GITHUB_REPOSITORY", ""), os.environ.get("GITHUB_RUN_ID", "")
    if not token or not repo or not owner.isdigit():
        print("::error::unit_watch needs GH_TOKEN, GITHUB_REPOSITORY and GITHUB_RUN_ID", file=sys.stderr)
        return 2
    run_url = f"{os.environ.get('GITHUB_SERVER_URL', 'https://github.com')}/{repo}/actions/runs/{owner}"
    gh = dp.GitHub(repo, token)
    failed = failed_lanes(args.deploy_results)
    if failed:
        pause(gh, "Automatic deploys paused: a unit deploy lane failed",
              f"## Unit deploy lane failed\n\nRun {run_url}: {', '.join(failed)} failed. A lane that fails after it "
              "activated a host rolls that host back to the release it ran before; one that fails earlier changed "
              "nothing. Find which in the run, then close this issue once the cause is reverted or fixed.\n")
    with tempfile.TemporaryDirectory() as workdir:
        env = {**os.environ, "WATCH_OWNER": owner}
        watch = Watch(Shell(str(HERE / "unit_deploy.sh"), env, args.compute_key, workdir), gh, owner,
                      args.deployctl, run_url)
        try:
            outcome = watch.run(adopt=args.adopt)
        except RuntimeError as err:
            outcome = Outcome(BREACH, f"## Post-deploy watch\n\n**Failed:** {err}\n")
    if args.summary:
        with open(args.summary, "a", encoding="utf-8") as f:
            f.write(outcome.report)
    print(outcome.report)
    if outcome.rolled_back:
        pause(gh, "Automatic deploys paused: post-deploy watch rolled back", outcome.report)
    return 0 if outcome.code == PASS and not failed else 1


def pause(gh, title: str, body: str) -> None:
    try:
        url = freeze.open_freeze_issue(gh, title, body)
        print(f"::warning::automatic deploys are paused: {url}")
    except (OSError, urllib.error.URLError, KeyError) as err:
        print(f"::error::could not open the deploy-freeze issue: {err}; freeze deploys by hand "
              "(docs/runbooks/DeployFreeze.md)")


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
