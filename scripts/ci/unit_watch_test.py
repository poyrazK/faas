#!/usr/bin/env python3
"""Unit tests for unit_watch.py (the post-deploy watch's runner side) and
deploy_freeze.py, with a fake control plane, fake nodes and a fake GitHub
(docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md)."""
import contextlib
import importlib.util
import io
import json
import pathlib
import sys
import tempfile
import unittest
import urllib.error

HERE = pathlib.Path(__file__).parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


uw = load("unit_watch")
df = load("deploy_freeze")

OWNER = "500"
G, A, B, N0, N1 = "0" * 40, "a" * 40, "c" * 40, "e" * 40, "f" * 40
TARGET = {"node": "fsn-2", "ssh_host": "10.0.0.2", "ssh_user": "root", "ssh_port": "22",
          "ssh_host_key_sha256": "SHA256:" + "x" * 43}


def record(owner=OWNER):
    return {
        "kind": "gregale-unit-watch", "owner_run": owner, "order": ["control-plane", "fsn-2"],
        "hosts": {
            "control-plane": {"known_good": G, "current": B, "releases": [A, B], "services": ["apid", "gatewayd-public"],
                              "verify": [], "gate": [["--unit", "apid"]], "commits": ["1" * 40], "target": None},
            "fsn-2": {"known_good": N0, "current": N1, "releases": [N1], "services": ["gatewayd-internal"],
                      "verify": [], "gate": [["--unit", "gatewayd-internal"]], "commits": ["2" * 40], "target": TARGET},
        },
    }


class FakeProc:
    """The remote watch: runs `polls` rounds, prints lines, exits code."""

    def __init__(self, code, polls=2, lines=()):
        self.code, self.polls, self.returncode = code, polls, None
        self.stdout = io.StringIO("".join(f"{line}\n" for line in lines))
        self.terminated = False

    def poll(self):
        if self.returncode is None and self.polls <= 0:
            self.returncode = self.code
        self.polls -= 1
        return self.returncode

    def terminate(self):
        self.terminated = True
        self.returncode = -15

    def wait(self):
        while self.poll() is None:
            pass
        return self.returncode


class FakeShell:
    def __init__(self, records, proc, rollback_codes=None, pin_ok=True):
        self.records = list(records)  # successive watch-record answers
        self.proc, self.rollback_codes = proc, rollback_codes or {}
        self.pin_ok, self.calls, self.workdir = pin_ok, [], tempfile.mkdtemp()
        self.compute_key = "/keys/compute"
        self.env = {"UNIT_SSH_TARGET": "root@cp"}

    def ud(self, args, env=None, timeout=600):
        host = (env or self.env)["UNIT_SSH_TARGET"]
        self.calls.append((args[0], host))
        if args[0] == "watch-record":
            return 0, json.dumps(self.records.pop(0) if len(self.records) > 1 else self.records[0]), ""
        if args[0] == "watch-close":
            return 0, "null", ""
        if args[0] == "watch-adopt":
            return 0, "{}", ""
        if args[0] == "pin-node":
            return (0, "", "") if self.pin_ok else (2, "", "no host key with fingerprint")
        if args[0] == "watch-stage":
            return 0, "/var/tmp/gregale-units.St4ge\n", ""
        if args[0] == "watch-rollback":
            entry = json.loads(pathlib.Path(args[1]).read_text())
            code = self.rollback_codes.get(entry["current"], 0)
            return code, "", "runs zzz, not it: changed since the watch record; left alone" if code == 5 else ""
        if args[0] == "cleanup":
            return 0, "", ""
        raise AssertionError(f"unexpected unit_deploy.sh {args}")

    def start(self, args):
        assert args == ["watch"]
        self.calls.append(("watch", "root@cp"))
        return self.proc

    node_env = uw.Shell.node_env


class FakeGitHub:
    def __init__(self, light=None, deploying=None):
        self.light = light or {}
        self.deploying = deploying or []
        self.posts = []
        self.labels = set()

    def get(self, path, params=None):
        if path.endswith(f"{uw.dp.LIGHT_WORKFLOW}/runs"):
            return {"workflow_runs": self.light.get(params["head_sha"], [])}
        if path == "actions/runs":
            return {"workflow_runs": self.deploying if params["status"] == "in_progress" else []}
        if path.startswith("labels/"):
            if path.split("/", 1)[1] in self.labels:
                return {"name": path.split("/", 1)[1]}
            raise urllib.error.HTTPError(path, 404, "Not Found", {}, io.BytesIO(b""))
        raise AssertionError(path)

    def post(self, path, body):
        self.posts.append((path, body))
        if path == "labels":
            self.labels.add(body["name"])
            return body
        return {"html_url": "https://github.example/issues/9"}


class WatchTest(unittest.TestCase):
    def watch(self, shell, gh=None):
        self.logs = []
        return uw.Watch(shell, gh or FakeGitHub(), OWNER, "/units/tools/deployctl", "https://github.example/run",
                        poll=60, now=lambda: 0, sleep=lambda s: None, log=self.logs.append)

    def test_nothing_to_watch(self):
        out = self.watch(FakeShell([None], FakeProc(0))).run()
        self.assertEqual(out.code, uw.PASS)
        self.assertIn("Nothing to watch", out.report)

    def test_a_newer_run_owns_the_record_before_this_watch_starts(self):
        shell = FakeShell([record(owner="600")], FakeProc(0))
        out = self.watch(shell).run()
        self.assertEqual(out.code, uw.PASS)
        self.assertIn("Run 600 took the watch over", out.report)
        self.assertNotIn(("watch", "root@cp"), shell.calls)

    def test_pass_closes_the_record(self):
        shell = FakeShell([record()], FakeProc(0))
        out = self.watch(shell).run()
        self.assertEqual((out.code, out.rolled_back), (uw.PASS, False))
        self.assertIn("**Passed**", out.report)
        self.assertIn(("watch-close", "root@cp"), shell.calls)

    def test_breach_rolls_every_host_back_newest_change_first(self):
        shell = FakeShell([record(), record()], FakeProc(1, lines=["::error::slo-gate: FaasApiAvailabilityLow true for 600s"]))
        out = self.watch(shell).run()
        self.assertEqual((out.code, out.rolled_back), (uw.BREACH, True))
        rollbacks = [host for name, host in shell.calls if name == "watch-rollback"]
        self.assertEqual(rollbacks, ["root@10.0.0.2", "root@cp"])  # the node first, then the control plane
        self.assertIn(("pin-node", "root@cp"), shell.calls)  # the node's host key is pinned first
        self.assertEqual(shell.calls[-1], ("watch-close", "root@cp"))
        self.assertIn("FaasApiAvailabilityLow", out.report)
        self.assertIn(f"on known-good {G[:12]}", out.report)
        self.assertIn("restarted apid, gatewayd-public", out.report)

    def test_a_failed_post_merge_light_run_is_a_breach(self):
        proc = FakeProc(0, polls=100)
        gh = FakeGitHub(light={"2" * 40: [{"status": "completed", "conclusion": "failure", "html_url": "https://l/1"}]})
        shell = FakeShell([record(), record()], proc)
        out = self.watch(shell, gh).run()
        self.assertTrue(proc.terminated)
        self.assertEqual((out.code, out.rolled_back), (uw.BREACH, True))
        self.assertIn("light tier failed on 222222222222", out.report)

    def test_a_deploy_in_flight_makes_the_watch_stand_down(self):
        proc = FakeProc(0, polls=100)
        gh = FakeGitHub(deploying=[{"id": 9, "name": "cd-platform", "path": ".github/workflows/cd-platform.yml",
                                    "html_url": "https://r/9"}])
        shell = FakeShell([record()], proc)
        out = self.watch(shell, gh).run()
        self.assertTrue(proc.terminated)
        self.assertEqual((out.code, out.rolled_back), (uw.PASS, False))
        self.assertIn("cd-platform run 9 (https://r/9) is deploying", out.report)
        self.assertNotIn(("watch-close", "root@cp"), shell.calls)

    def test_superseded_by_a_newer_run(self):
        shell = FakeShell([record()], FakeProc(4, lines=["slo-gate: stopping: run 600 now owns the watch"]))
        out = self.watch(shell).run()
        self.assertEqual((out.code, out.rolled_back), (uw.PASS, False))
        self.assertIn("run 600 now owns the watch", out.report)
        self.assertNotIn(("watch-close", "root@cp"), shell.calls)

    def test_inconclusive_neither_promotes_nor_rolls_back(self):
        shell = FakeShell([record()], FakeProc(3))
        out = self.watch(shell).run()
        self.assertEqual((out.code, out.rolled_back), (uw.BREACH, False))
        self.assertIn("Inconclusive", out.report)
        self.assertFalse([c for c in shell.calls if c[0] in ("watch-close", "watch-rollback")])

    def test_no_rollback_once_another_run_owns_the_record(self):
        shell = FakeShell([record(), record(owner="600")], FakeProc(1))
        out = self.watch(shell).run()
        self.assertEqual((out.code, out.rolled_back), (uw.BREACH, False))
        self.assertIn("run 600 took the watch over before the rollback", out.report)
        self.assertFalse([c for c in shell.calls if c[0] == "watch-rollback"])

    def test_a_host_changed_since_the_record_is_left_alone(self):
        shell = FakeShell([record(), record()], FakeProc(1), rollback_codes={N1: 5})
        out = self.watch(shell).run()
        self.assertIn("fsn-2: left alone", out.report)
        self.assertIn("control-plane: on known-good", out.report)

    def test_a_node_whose_host_key_does_not_match_is_not_touched(self):
        shell = FakeShell([record(), record()], FakeProc(1), pin_ok=False)
        out = self.watch(shell).run()
        self.assertIn("fsn-2: not rolled back: cannot verify fsn-2's host key", out.report)
        self.assertEqual([h for n, h in shell.calls if n == "watch-rollback"], ["root@cp"])

    def test_adopt_takes_the_record_first(self):
        shell = FakeShell([record()], FakeProc(0))
        self.watch(shell).run(adopt=True)
        self.assertEqual(shell.calls[0], ("watch-adopt", "root@cp"))


class MainTest(unittest.TestCase):
    def test_failed_lanes(self):
        self.assertEqual(uw.failed_lanes("deploy=success,deploy-edge=failure"), ["deploy-edge"])
        self.assertEqual(uw.failed_lanes("deploy=failure, deploy-edge=skipped"), ["deploy"])
        self.assertEqual(uw.failed_lanes(""), [])

    def test_a_failed_lane_pauses_deploys_before_the_watch(self):
        gh, issues = FakeGitHub(), []
        orig = (uw.dp.GitHub, uw.Watch.run, uw.freeze.open_freeze_issue)
        uw.dp.GitHub = lambda repo, token: gh
        uw.Watch.run = lambda self, adopt=False: uw.Outcome(uw.PASS, "## Post-deploy watch\n\nNothing to watch.\n")
        uw.freeze.open_freeze_issue = lambda g, title, body: issues.append((title, body)) or "https://i/1"
        env = {"GH_TOKEN": "t", "GITHUB_REPOSITORY": "o/r", "GITHUB_RUN_ID": OWNER}
        saved = {k: uw.os.environ.get(k) for k in env}
        uw.os.environ.update(env)
        try:
            with tempfile.NamedTemporaryFile("w+") as summary, contextlib.redirect_stdout(io.StringIO()):
                code = uw.main(["--deployctl", "/d", "--deploy-results", "deploy=success,deploy-edge=failure",
                                "--summary", summary.name])
        finally:
            uw.dp.GitHub, uw.Watch.run, uw.freeze.open_freeze_issue = orig
            for k, v in saved.items():
                if v is None:
                    uw.os.environ.pop(k, None)
                else:
                    uw.os.environ[k] = v
        self.assertEqual(code, 1)
        self.assertEqual(len(issues), 1)
        self.assertIn("deploy-edge failed", issues[0][1])


class FreezeIssueTest(unittest.TestCase):
    def test_creates_the_label_once_and_opens_a_labelled_issue(self):
        gh = FakeGitHub()
        url = df.open_freeze_issue(gh, "Automatic deploys paused", "report")
        self.assertEqual(url, "https://github.example/issues/9")
        self.assertEqual(gh.posts[0], ("labels", {"name": "deploy-freeze", "color": df.LABEL_COLOR,
                                                  "description": df.LABEL_DESCRIPTION}))
        path, body = gh.posts[1]
        self.assertEqual((path, body["labels"]), ("issues", ["deploy-freeze"]))
        self.assertIn("Close it once the cause is reverted or fixed", body["body"])
        df.open_freeze_issue(gh, "again", "report")
        self.assertEqual([p for p, _ in gh.posts].count("labels"), 1)

    def test_label_matches_the_preflight(self):
        self.assertEqual(df.LABEL, uw.dp.FREEZE_LABEL)


if __name__ == "__main__":
    unittest.main()
