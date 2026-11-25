#!/usr/bin/env python3
"""Unit tests for deploy_preflight.py against a fake GitHub API and a fake
control plane (docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md)."""
import contextlib
import importlib.util
import io
import pathlib
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("deploy_preflight", HERE / "deploy_preflight.py")
dp = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = dp  # dataclasses resolve annotations through sys.modules
spec.loader.exec_module(dp)

SHA = "a" * 40
ME = 100


def run_obj(run_id, number, path="release-units.yml", status="in_progress", sha="b" * 40, name=None):
    return {"id": run_id, "run_number": number, "status": status, "head_sha": sha,
            "path": f".github/workflows/{path}", "name": name or path.removesuffix(".yml"),
            "html_url": f"https://github.example/runs/{run_id}"}


def job(name, status="completed", conclusion="success"):
    return {"name": name, "status": status, "conclusion": conclusion}


class FakeGitHub:
    """Answers the paths deploy_preflight reads from mutable state."""

    def __init__(self):
        self.issues = []
        self.unit_runs = [run_obj(ME, 50, sha=SHA)]
        self.active = {"in_progress": [run_obj(ME, 50, sha=SHA)], "queued": [], "waiting": []}
        # workflow file -> its newest runs (actions/workflows/{file}/runs).
        self.workflow_runs = {}
        self.jobs = {}
        self.light = []
        self.runs = {}
        self.requests = []
        # Runs that uploaded the release marker for SHA.
        self.released_by = []
        # run id -> the commit its marker names (actions/runs/{id}/artifacts).
        self.markers = {}
        # run id -> the full release its base marker names.
        self.bases = {}

    def get(self, path, params=None):
        self.requests.append(path)
        params = params or {}
        if path == "actions/artifacts":
            assert params["name"] == dp.RELEASE_MARKER + SHA
            return {"artifacts": [{"name": params["name"], "workflow_run": {"id": r}} for r in self.released_by]}
        if path == "issues":
            assert params["labels"] == dp.FREEZE_LABEL and params["state"] == "open"
            return self.issues
        if path == f"actions/runs/{ME}":
            return run_obj(ME, 50, sha=SHA)
        if path == f"actions/workflows/{dp.UNITS_WORKFLOW}/runs":
            return {"workflow_runs": self.unit_runs}
        if path == f"actions/workflows/{dp.LIGHT_WORKFLOW}/runs":
            assert params["head_sha"] == SHA
            return {"workflow_runs": self.light}
        if path == "actions/runs":
            return {"workflow_runs": self.active[params["status"]]}
        if path.startswith("actions/workflows/") and path.endswith("/runs") and path.split("/")[2] in dp.DEPLOY_WORKFLOWS:
            return {"workflow_runs": self.workflow_runs.get(path.split("/")[2], [])}
        if path.startswith("actions/runs/") and path.endswith("/artifacts"):
            run = int(path.split("/")[2])
            sha, base = self.markers.get(run), self.bases.get(run)
            return {"artifacts": ([{"name": dp.RELEASE_MARKER + sha}] if sha else []) +
                                 ([{"name": dp.BASE_MARKER + base}] if base else [])}
        if path in self.runs:
            return self.runs[path]
        if path.startswith("actions/runs/") and path.endswith("/jobs"):
            return {"jobs": self.jobs.get(int(path.split("/")[2]), [])}
        raise AssertionError(f"unexpected GitHub path {path}")


def healthy_host(alerts=(), lock="free"):
    return {"lock": lock, "prometheus": {"ok": True, "alerts": list(alerts)}}


def alert(name, severity="page", state="firing", family=None):
    labels = {"alertname": name, "severity": severity, "component": "apid"}
    if family:
        labels["family"] = family
    return {"labels": labels, "state": state, "activeAt": "2026-10-20T10:00:00Z"}


class Clock:
    def __init__(self):
        self.t = 1_800_000_000.0

    def now(self):
        return self.t

    def sleep(self, s):
        self.t += s


class PreflightCase(unittest.TestCase):
    def setUp(self):
        self.gh = FakeGitHub()
        self.host = healthy_host()
        self.clock = Clock()
        self.logs = []
        self.on_sleep = None

    def run_preflight(self, wait=600, interval=60, follow=False):
        pre = dp.Preflight(self.gh, lambda: self.host, ME, "" if follow else SHA, log=self.logs.append,
                           follow_build=follow, full_every=interval, clock=self.clock.now)

        def sleep(s):
            self.clock.sleep(s)
            if self.on_sleep:
                self.on_sleep(self)

        return dp.run(pre, wait, interval, now=self.clock.now, sleep=sleep, log=self.logs.append,
                      poll=10 if follow else None)


class PreflightTest(PreflightCase):
    def test_go_when_idle_healthy_and_unfrozen(self):
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, True))
        self.assertIn("**Go.**", summary)
        self.assertEqual(self.clock.t, 1_800_000_000.0, "a clear platform must not wait")

    def test_freeze_issue_holds_without_failing(self):
        self.gh.issues = [{"number": 7, "title": "Autodeploy paused", "html_url": "https://github.example/issues/7"}]
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, False))
        self.assertIn("Held, not deployed", summary)
        self.assertIn("#7 Autodeploy paused", summary)

    CP_LANE = "Deploy the control-plane lane / Deploy unit release to Control Plane Host"
    EDGE_LANE = "Deploy the edge lane / Roll the edge"

    def test_commit_an_earlier_run_deployed_through_every_lane_stands_down(self):
        # A stalled lane releases the same commit on every merge.
        self.gh.released_by = [90]
        self.gh.jobs[90] = [job(self.CP_LANE), job(self.EDGE_LANE), job("Watch the deployed units / watch")]
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, False))
        self.assertIn("run 90 already deployed the unit release", summary)

    def test_edge_only_or_skipped_lane_counts_by_the_lanes_that_ran(self):
        self.gh.released_by = [90]
        self.gh.jobs[90] = [job(self.CP_LANE, conclusion="skipped"), job(self.EDGE_LANE)]
        self.assertEqual(self.run_preflight()[:2], (0, False))

    def test_failed_or_unfinished_lane_is_deployed_again(self):
        for jobs in ([job(self.CP_LANE), job(self.EDGE_LANE, conclusion="failure")],
                     [job(self.CP_LANE, conclusion="failure")],
                     [job(self.CP_LANE), job(self.EDGE_LANE, status="in_progress", conclusion=None)],
                     [job("Check production can take a deploy")]):
            with self.subTest(jobs=jobs):
                self.gh.released_by = [90]
                self.gh.jobs[90] = jobs
                self.assertEqual(self.run_preflight()[:2], (0, True))

    def test_this_run_and_no_earlier_release_go(self):
        for released in ([], [ME]):
            with self.subTest(released=released):
                self.gh.released_by = released
                self.gh.jobs[ME] = [job(self.CP_LANE)]
                self.assertEqual(self.run_preflight()[:2], (0, True))

    def test_pull_requests_with_the_label_do_not_freeze(self):
        self.gh.issues = [{"number": 8, "title": "pr", "html_url": "x", "pull_request": {}}]
        self.assertEqual(self.run_preflight()[:2], (0, True))

    def test_failed_post_merge_light_run_fails(self):
        self.gh.light = [{"status": "completed", "conclusion": "failure", "html_url": "https://github.example/light"}]
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (1, False))
        self.assertIn("light tier failed", summary)

    def test_unfinished_or_replaced_light_run_does_not_block(self):
        for light in ([], [{"status": "in_progress", "conclusion": None}], [{"status": "completed", "conclusion": "cancelled"}]):
            with self.subTest(light=light):
                self.gh.light = light
                self.assertEqual(self.run_preflight()[:2], (0, True))

    def test_newer_deploying_run_takes_over(self):
        self.gh.unit_runs.append(run_obj(101, 51))
        self.gh.jobs[101] = [job(dp.BUILD_JOB), job(dp.PREFLIGHT_JOB, status="in_progress", conclusion=None)]
        self.gh.markers[101] = "c" * 40
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, False))
        self.assertIn("Stood down", summary)

    def test_newer_run_that_deploys_nothing_does_not_take_over(self):
        cases = {
            "build only": ([job(dp.BUILD_JOB), job(dp.PREFLIGHT_JOB, conclusion="skipped")], "c" * 40),
            "released nothing": ([job(dp.BUILD_JOB), job(dp.PREFLIGHT_JOB, status="in_progress", conclusion=None)], None),
            "build failed": ([job(dp.BUILD_JOB, conclusion="failure"), job(dp.PREFLIGHT_JOB, conclusion="success")], None),
            "build-only run still building": ([job(dp.BUILD_JOB, status="in_progress", conclusion=None),
                                               job(dp.PREFLIGHT_JOB, conclusion="skipped")], None),
        }
        for name, (jobs, marker) in cases.items():
            with self.subTest(name):
                self.gh.unit_runs = [run_obj(ME, 50, sha=SHA), run_obj(101, 51)]
                self.gh.jobs[101], self.gh.markers = jobs, {101: marker} if marker else {}
                self.assertEqual(self.run_preflight()[:2], (0, True))

    def test_waits_for_a_newer_build_then_stands_down(self):
        self.gh.unit_runs.append(run_obj(101, 51))
        # Its preflight starts beside its build.
        self.gh.jobs[101] = [job(dp.BUILD_JOB, status="in_progress", conclusion=None),
                             job(dp.PREFLIGHT_JOB, status="in_progress", conclusion=None)]

        def build_done(t):
            t.gh.jobs[101] = [job(dp.BUILD_JOB), job(dp.PREFLIGHT_JOB, status="in_progress", conclusion=None)]
            t.gh.markers[101] = "c" * 40
        self.on_sleep = build_done
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, False))
        self.assertIn("newest: ", summary)  # the wait is reported

    def test_other_deploy_workflow_makes_it_wait_and_reports(self):
        self.gh.active["in_progress"].append(run_obj(200, 9, path="cd-platform.yml", name="cd-platform"))

        def done(t):
            if t.clock.t >= 1_800_000_000.0 + 180:
                t.gh.active["in_progress"] = [run_obj(ME, 50, sha=SHA)]
        self.on_sleep = done
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, True))
        self.assertIn("cd-platform run 200", summary)
        self.assertIn("idle: 180 s", summary)
        self.assertTrue(any("::notice::" in line and "cd-platform run 200" in line for line in self.logs))

    def test_a_deploy_run_the_status_listing_misses_still_makes_it_wait(self):
        # 2026-11-25: the repository-wide listing dropped cd-platform run
        # 45639008753 while it rolled rc.381 out (#8070).
        self.gh.workflow_runs["cd-platform.yml"] = [
            run_obj(201, 10, path="cd-platform.yml", name="cd-platform"),
            run_obj(199, 8, path="cd-platform.yml", name="cd-platform", status="completed")]

        def done(t):
            if t.clock.t >= 1_800_000_000.0 + 120:
                t.gh.workflow_runs["cd-platform.yml"][0]["status"] = "completed"
        self.on_sleep = done
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, True))
        self.assertIn("cd-platform run 201", summary)
        self.assertNotIn("run 199", summary)

    def test_other_unit_run_deploying_makes_it_wait_but_its_watch_does_not(self):
        self.gh.active["in_progress"].append(run_obj(90, 49, status="in_progress"))
        self.gh.jobs[90] = [job(dp.BUILD_JOB), job(dp.PREFLIGHT_JOB),
                            job("Deploy the control-plane lane / Deploy unit release to Control Plane Host",
                                status="in_progress", conclusion=None)]
        code, go, _ = self.run_preflight(wait=120)
        self.assertEqual((code, go), (1, False))
        self.gh.jobs[90][2] = job("Deploy the control-plane lane / Deploy unit release to Control Plane Host")
        self.gh.jobs[90].append(job("Watch the deployed units / Watch", status="in_progress", conclusion=None))
        self.assertEqual(self.run_preflight()[:2], (0, True))

    def test_held_deploy_lock_waits(self):
        self.host = healthy_host(lock="held")
        code, go, summary = self.run_preflight(wait=120)
        self.assertEqual((code, go), (1, False))
        self.assertIn("deploy lock is held", summary)
        self.assertIn("the next merge tries again", summary)

    def test_page_alert_waits_until_it_resolves(self):
        self.host = healthy_host([alert("FaasDaemonDown")])

        def resolve(t):
            t.host = healthy_host()
        self.on_sleep = resolve
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, True))
        self.assertIn("FaasDaemonDown", summary)

    def test_only_firing_page_alerts_outside_tenant_families_hold(self):
        self.host = healthy_host([
            alert("FaasWarnThing", severity="warn"),
            alert("FaasPendingPage", state="pending"),
            alert("FaasTrafficSpikeAccount", family="traffic_anomaly"),
        ])
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, True))
        self.assertIn("tenant-signal alerts ignored: FaasTrafficSpikeAccount", summary)

    def test_tenant_families_come_from_the_slo_gate(self):
        self.assertIn("traffic_anomaly", dp.TENANT_SIGNAL_FAMILIES)
        self.assertIn("residency", dp.TENANT_SIGNAL_FAMILIES)

    def test_unreachable_prometheus_or_host_waits(self):
        for host in ({"lock": "free", "prometheus": {"ok": False, "error": "connection refused"}}, {"error": "ssh: timeout"}):
            with self.subTest(host=host):
                self.host = host
                code, go, summary = self.run_preflight(wait=120)
                self.assertEqual((code, go), (1, False))
                self.assertIn("healthy", summary)

    def test_host_state_errors_are_caught(self):
        def broken():
            raise OSError("ssh exited 255")
        pre = dp.Preflight(self.gh, broken, ME, SHA, log=self.logs.append)
        verdicts = {v.check: v for v in pre.evaluate()}
        self.assertEqual(verdicts["idle"].state, dp.WAIT)
        self.assertEqual(verdicts["healthy"].state, dp.WAIT)

    def test_github_errors_wait_then_give_up(self):
        def broken(path, params=None):
            raise OSError("api down")
        self.gh.get = broken
        code, go, summary = self.run_preflight(wait=120)
        self.assertEqual((code, go), (1, False))
        self.assertIn("GitHub API: api down", summary)

    def test_light_verdict_is_read_once_it_is_final(self):
        self.gh.light = [{"status": "completed", "conclusion": "success"}]
        self.host = healthy_host(lock="held")
        self.run_preflight(wait=300)
        light_reads = [p for p in self.gh.requests if p.endswith(f"{dp.LIGHT_WORKFLOW}/runs")]
        self.assertEqual(len(light_reads), 1)

    def test_api_requests_per_round_stay_within_the_token_budget(self):
        self.host = healthy_host(lock="held")
        self.run_preflight(wait=40 * 60, interval=60)
        # 1,000 requests an hour per repository for the workflow token.
        self.assertLess(len(self.gh.requests), 400)


class FollowBuildTest(PreflightCase):
    """--follow-build: the preflight starts beside this run's build
    (ADR-20261122170000, "Preflight beside the build")."""

    def building(self):
        self.gh.jobs[ME] = [job(dp.BUILD_JOB, status="in_progress", conclusion=None)]

    def finish_at(self, t_done, conclusion="success", marker=SHA):
        def step(t):
            if t.clock.t >= 1_800_000_000.0 + t_done:
                t.gh.jobs[ME] = [job(dp.BUILD_JOB, conclusion=conclusion)]
                if marker:
                    t.gh.markers[ME] = marker
        self.on_sleep = step

    def test_goes_within_one_poll_of_the_build_ending_on_a_fresh_round(self):
        self.building()
        self.finish_at(95)
        rounds = []
        orig = self.gh.get

        def counting(path, params=None):
            if path == "issues":
                rounds.append(self.clock.t)
            return orig(path, params)
        self.gh.get = counting
        code, go, summary = self.run_preflight(follow=True)
        self.assertEqual((code, go), (0, True))
        self.assertEqual(self.clock.t, 1_800_000_000.0 + 100, "the poll after the build ended goes")
        self.assertEqual(rounds[-1], self.clock.t, "the go comes from a full round taken after the build")
        self.assertIn(f"this run released {SHA[:12]}", summary)

    def test_other_checks_are_reread_every_interval_while_building(self):
        self.building()
        self.finish_at(300)
        self.run_preflight(follow=True, interval=60)
        freeze_reads = [p for p in self.gh.requests if p == "issues"]
        self.assertEqual(len(freeze_reads), 6, "t=0,60,...,240 while building, then t=300")

    def test_sha_checks_read_the_released_commit(self):
        self.building()
        self.finish_at(10)
        self.gh.light = [{"status": "completed", "conclusion": "failure", "html_url": "https://github.example/light"}]
        code, go, summary = self.run_preflight(follow=True)
        self.assertEqual((code, go), (1, False))
        self.assertIn(f"light tier failed on {SHA[:12]}", summary)

    def test_failed_or_empty_build_stands_down(self):
        for conclusion, marker, want in (("failure", None, "ended failure"), ("cancelled", None, "ended cancelled"),
                                         ("success", None, "released nothing")):
            with self.subTest(conclusion=conclusion):
                self.setUp()
                self.building()
                self.finish_at(20, conclusion, marker)
                code, go, summary = self.run_preflight(follow=True)
                self.assertEqual((code, go), (0, False))
                self.assertIn(want, summary)

    def test_freeze_holds_without_waiting_for_the_build(self):
        self.building()
        self.gh.issues = [{"number": 7, "title": "paused", "html_url": "x"}]
        self.assertEqual(self.run_preflight(follow=True)[:2], (0, False))
        self.assertEqual(self.clock.t, 1_800_000_000.0)

    def test_busy_production_polls_at_the_interval_not_the_poll(self):
        self.gh.jobs[ME] = [job(dp.BUILD_JOB)]
        self.gh.markers[ME] = SHA
        self.host = healthy_host(lock="held")
        self.run_preflight(follow=True, wait=300, interval=60)
        self.assertEqual(len([p for p in self.gh.requests if p == "issues"]), 6)


class BaseTest(PreflightCase):
    """The control plane must run the full release the unit release is
    built over; a newer tag not yet deployed stands the lane down."""
    BASE = "e" * 40

    def built(self, base, host_base):
        self.gh.jobs[ME] = [job(dp.BUILD_JOB)]
        self.gh.markers[ME] = SHA
        if base:
            self.gh.bases[ME] = base
        self.host = healthy_host()
        if host_base is not None:
            self.host["base"] = host_base

    def test_same_base_goes(self):
        self.built(self.BASE, self.BASE)
        code, go, summary = self.run_preflight(follow=True)
        self.assertEqual((code, go), (0, True))
        self.assertIn("runs the full release this unit release is built over", summary)

    def test_an_undeployed_newer_tag_stands_down_without_failing(self):
        self.built(self.BASE, "f" * 40)
        code, go, summary = self.run_preflight(follow=True)
        self.assertEqual((code, go), (0, False))
        self.assertIn("deploy that release with cd-platform first", summary)
        self.assertIn("Stood down", summary)

    def test_unknown_base_leaves_it_to_the_planner(self):
        for base, host_base in ((self.BASE, None), (self.BASE, ""), (None, self.BASE)):
            with self.subTest(base=base, host_base=host_base):
                self.setUp()
                self.built(base, host_base)
                self.assertEqual(self.run_preflight(follow=True)[:2], (0, True))


class WatchCapTest(PreflightCase):
    def watched(self, releases, owner="90"):
        self.host = healthy_host()
        self.host["watch"] = {"owner_run": owner, "hosts": {"control-plane": {"releases": releases}}}

    def full(self):
        """As many unwatched releases as the cap allows (release_units.json)."""
        return ["abcdef0123456789"[i] * 40 for i in range(dp.MAX_UNWATCHED)]

    def test_open_watch_below_the_cap_lets_the_deploy_join_it(self):
        self.watched(["a" * 40])
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, True))
        self.assertIn("run 90's watch covers the unwatched releases", summary)

    def test_at_the_cap_waits_for_the_running_watch(self):
        self.watched(self.full())
        self.gh.runs["actions/runs/90"] = {"status": "in_progress", "html_url": "https://r/90"}

        def watch_done(t):
            if t.clock.t >= 1_800_000_000.0 + 600:
                t.host["watch"] = None
        self.on_sleep = watch_done
        code, go, summary = self.run_preflight(wait=40 * 60)
        self.assertEqual((code, go), (0, True))
        self.assertIn("waiting for run 90's post-deploy watch", summary)
        self.assertIn("watch: 600 s", summary)

    def test_at_the_cap_with_no_watch_running_fails_and_says_how_to_adopt(self):
        self.watched(self.full())
        self.gh.runs["actions/runs/90"] = {"status": "completed", "conclusion": "failure"}
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (1, False))
        self.assertIn("adopt=true", summary)

    def test_cap_comes_from_the_release_units_config(self):
        self.assertEqual(dp.MAX_UNWATCHED, 4)


class MainTest(unittest.TestCase):
    def test_usage_error_without_token(self):
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(dp.main(["--repo", "o/r", "--run-id", "1", "--sha", SHA]), 2)

    def test_usage_error_without_sha_unless_following_the_build(self):
        from unittest import mock
        with mock.patch.dict("os.environ", {"GH_TOKEN": "t"}), contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(dp.main(["--repo", "o/r", "--run-id", "1"]), 2)

    def test_writes_summary_and_output(self):
        gh = FakeGitHub()
        with tempfile.TemporaryDirectory() as d:
            summary, output = pathlib.Path(d, "summary"), pathlib.Path(d, "output")
            orig_github, orig_host = dp.GitHub, dp.host_state_command
            dp.GitHub = lambda repo, token: gh
            dp.host_state_command = lambda script: healthy_host()
            try:
                import os
                os.environ["GH_TOKEN"] = "t"
                code = dp.main(["--repo", "o/r", "--run-id", str(ME), "--sha", SHA,
                                "--summary", str(summary), "--output", str(output)])
            finally:
                dp.GitHub, dp.host_state_command = orig_github, orig_host
            self.assertEqual(code, 0)
            self.assertEqual(output.read_text(), "go=true\n")
            self.assertIn("## Deploy preflight", summary.read_text())

    def test_parse_duration(self):
        self.assertEqual(dp.parse_duration("40m"), 2400)
        self.assertEqual(dp.parse_duration("30s"), 30)
        self.assertEqual(dp.parse_duration("90"), 90)


if __name__ == "__main__":
    unittest.main()
