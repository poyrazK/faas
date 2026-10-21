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
        self.jobs = {}
        self.light = []
        self.runs = {}
        self.requests = []

    def get(self, path, params=None):
        self.requests.append(path)
        params = params or {}
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

    def run_preflight(self, wait=600, interval=60):
        pre = dp.Preflight(self.gh, lambda: self.host, ME, SHA, log=self.logs.append)

        def sleep(s):
            self.clock.sleep(s)
            if self.on_sleep:
                self.on_sleep(self)

        return dp.run(pre, wait, interval, now=self.clock.now, sleep=sleep, log=self.logs.append)


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
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, False))
        self.assertIn("Stood down", summary)

    def test_newer_ineligible_run_does_not_take_over(self):
        self.gh.unit_runs.append(run_obj(101, 51, status="completed"))
        self.gh.jobs[101] = [job(dp.BUILD_JOB), job(dp.PREFLIGHT_JOB, conclusion="skipped")]
        self.assertEqual(self.run_preflight()[:2], (0, True))

    def test_waits_for_a_newer_build_then_stands_down(self):
        self.gh.unit_runs.append(run_obj(101, 51))
        self.gh.jobs[101] = [job(dp.BUILD_JOB, status="in_progress", conclusion=None)]

        def build_done(t):
            t.gh.jobs[101] = [job(dp.BUILD_JOB), job(dp.PREFLIGHT_JOB, status="queued", conclusion=None)]
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


class WatchCapTest(PreflightCase):
    def watched(self, releases, owner="90"):
        self.host = healthy_host()
        self.host["watch"] = {"owner_run": owner, "hosts": {"control-plane": {"releases": releases}}}

    def test_open_watch_below_the_cap_lets_the_deploy_join_it(self):
        self.watched(["a" * 40])
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (0, True))
        self.assertIn("run 90's watch covers the unwatched releases", summary)

    def test_at_the_cap_waits_for_the_running_watch(self):
        self.watched(["a" * 40, "c" * 40])
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
        self.watched(["a" * 40, "c" * 40])
        self.gh.runs["actions/runs/90"] = {"status": "completed", "conclusion": "failure"}
        code, go, summary = self.run_preflight()
        self.assertEqual((code, go), (1, False))
        self.assertIn("adopt=true", summary)

    def test_cap_comes_from_the_release_units_config(self):
        self.assertEqual(dp.MAX_UNWATCHED, 2)


class MainTest(unittest.TestCase):
    def test_usage_error_without_token(self):
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(dp.main(["--repo", "o/r", "--run-id", "1", "--sha", SHA]), 2)

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
