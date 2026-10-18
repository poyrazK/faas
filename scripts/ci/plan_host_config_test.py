#!/usr/bin/env python3
"""Unit tests for plan_host_config.py (ADR 20261018133100)."""
import importlib.util
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("plan_host_config", HERE / "plan_host_config.py")
phc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(phc)
hr = phc.host_roles

BASE, COMMIT = "b" * 40, "c" * 40
RELEASE = {"kind": "gregale-unit-release", "format_version": 2, "base_sha": BASE, "git_sha": COMMIT}
NEW = {"format_version": 1, "group": "control_plane", "globals": "sha256:g", "order": ["nftables", "postgres", "grafana"],
       "roles": {"nftables": "sha256:n", "postgres": "sha256:p2", "grafana": "sha256:f"}}


def record(commit="a" * 40, **changes):
    out = {"format_version": 1, "kind": "gregale-host-config", "group": "control_plane", "commit": commit,
           "globals": "sha256:g", "order": NEW["order"], "environment": "sha256:e",
           "roles": {"nftables": "sha256:n", "postgres": "sha256:p1", "grafana": "sha256:f"}}
    out.update(changes)
    return out


def host(rec, base=BASE):
    return {"base_git_sha": base, "current": base, "record": rec}


def ancestors(*pairs):
    return lambda a, d: (a, d) in pairs


class PlanTest(unittest.TestCase):
    CASES = [
        # name, host state, deps, action, roles, all, previous
        ("a changed role converges alone", host(record()), None, "converge", ["postgres"], False, "a" * 40),
        ("a notified role's owner comes along", host(record()), {"postgres": {"grafana"}},
         "converge", ["postgres", "grafana"], False, "a" * 40),
        ("nothing changed", host(record(roles=dict(NEW["roles"]))), None, "none", [], False, "a" * 40),
        ("no record converges every role once", host(None), None, "converge", NEW["order"], True, BASE),
        ("another record format converges every role", host(record(format_version=0)), None,
         "converge", NEW["order"], True, BASE),
        ("other globals converge every role", host(record(globals="sha256:old")), None,
         "converge", NEW["order"], True, "a" * 40),
    ]

    def test_cases(self):
        for name, state, deps, action, roles, every, previous in self.CASES:
            with self.subTest(name):
                out, rec = phc.plan(RELEASE, state, NEW, deps, ancestors(("a" * 40, COMMIT)))
                self.assertEqual(out["action"], action)
                self.assertEqual(out["roles"], roles)
                self.assertEqual(out["all"], every)
                self.assertEqual(out["previous"], previous)
                self.assertEqual(out["commit"], COMMIT)
                if action == "none":
                    self.assertIsNone(rec)
                else:
                    # The record names every role at the release commit;
                    # the workflow adds the environment hash after the gate.
                    self.assertEqual(rec, {"format_version": 1, "kind": "gregale-host-config", "group": "control_plane",
                                           "commit": COMMIT, "globals": "sha256:g", "order": NEW["order"],
                                           "roles": NEW["roles"]})

    def test_roles_that_can_restart_postgres(self):
        stateful = {"postgres": ["postgresql"]}
        base = dict(NEW, roles=dict(NEW["roles"]))
        ok = ancestors(("a" * 40, COMMIT))
        # postgres differs from the record but not from the full release,
        # which converged it: a partial plan leaves it alone.
        out, _ = phc.plan(RELEASE, host(record()), NEW, None, ok, None, stateful, base)
        self.assertEqual((out["action"], out["roles"], out["guarded"]), ("none", [], []))
        out, _ = phc.plan(RELEASE, host(record(roles=dict(NEW["roles"], grafana="x"))), NEW, None, ok, None,
                          stateful, base)
        self.assertEqual(out["roles"], ["grafana"])
        # Converging every role runs it under the lane's restart guard.
        out, _ = phc.plan(RELEASE, host(None), NEW, None, ok, None, stateful, base)
        self.assertEqual((out["roles"], out["guarded"]), (NEW["order"], ["postgres"]))
        # Changed since the full release: only a full release converges it.
        with self.assertRaisesRegex(phc.Refused, "postgres changed since the full release and can restart postgresql"):
            phc.plan(RELEASE, host(record()), NEW, None, ok, None, stateful, dict(base, roles=dict(NEW["roles"], postgres="old")))
        with self.assertRaisesRegex(phc.Refused, "globals changed since the full release"):
            phc.plan(RELEASE, host(record()), NEW, None, ok, None, stateful, dict(base, globals="sha256:old"))

    def test_refusals(self):
        cases = [
            ("not a unit release", dict(RELEASE, kind="other"), host(record()), None, "format 2"),
            ("N±1", RELEASE, host(record()), "d" * 40, "N±1"),
            ("host runs another full release", RELEASE, host(record(), base="e" * 40), None, "deploy that full release"),
            ("host converged newer content", RELEASE, host(record(commit="f" * 40)), None, "not an ancestor"),
        ]
        for name, release, state, previous, want in cases:
            with self.subTest(name):
                with self.assertRaisesRegex(phc.Refused, want):
                    phc.plan(release, state, NEW, None, ancestors(("a" * 40, COMMIT)), previous)


class GateTest(unittest.TestCase):
    def test_gate_holds_the_platform_and_every_restarted_daemon(self):
        report = {"restarted": [{"service": "meterd"}, {"service": "apid-clone-worker"}], "dry_run": False}
        self.assertEqual(phc.gate(report), [
            ["--unit", "host-config", "--component", "host-config"],
            ["--unit", "meterd"],
            ["--unit", "apid-clone-worker", "--component", "apid-clone-worker"],
        ])
        self.assertEqual(phc.gate({"restarted": None}), [["--unit", "host-config", "--component", "host-config"]])

    def test_every_gate_argument_passes_the_remote_runner_filter(self):
        # unit_deploy.sh gate refuses anything but flags and unit names.
        for args in phc.gate({"restarted": [{"service": s} for s in ("apid", "githubd", "gatewayd-public")]}):
            for arg in args:
                self.assertRegex(arg, r"^(--)?[a-z0-9-]+$")


class MainTest(unittest.TestCase):
    """The CLI against a throwaway repository with real role hashes."""

    def run_git(self, *args):
        return subprocess.run(["git", *args], cwd=self.root, check=True, capture_output=True, text=True).stdout.strip()

    def write(self, path, body):
        full = pathlib.Path(self.root, path)
        full.parent.mkdir(parents=True, exist_ok=True)
        full.write_text(body, encoding="utf-8")

    def commit(self):
        self.run_git("add", "-A")
        self.run_git("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "x")
        return self.run_git("rev-parse", "HEAD")

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = self.tmp.name
        self.run_git("init", "-q")
        self.write("deploy/ansible/bootstrap.yml",
                   "- name: cp\n  hosts: control_plane\n  roles:\n    - role: postgres_capacity\n    - role: grafana\n"
                   "- name: compute\n  hosts: compute_nodes\n  roles:\n    - role: grafana\n")
        self.write("deploy/ansible/roles/postgres_capacity/defaults/main.yml", "budget: 47\n")
        self.write("deploy/ansible/roles/grafana/tasks/main.yml", "- debug: {}\n")
        self.base = self.commit()
        self.write("deploy/ansible/roles/postgres_capacity/defaults/main.yml", "budget: 49\n")
        self.head = self.commit()

    def plan(self, rec):
        out = os.path.join(self.root, "out")
        manifest = os.path.join(self.root, "m.json")
        state = os.path.join(self.root, "h.json")
        with open(manifest, "w", encoding="utf-8") as fh:
            json.dump(dict(RELEASE, base_sha=self.base, git_sha=self.head), fh)
        with open(state, "w", encoding="utf-8") as fh:
            json.dump({"base_git_sha": self.base, "current": self.base, "record": rec}, fh)
        proc = subprocess.run([sys.executable, str(HERE / "plan_host_config.py"), "--units-manifest", manifest,
                               "--host-state", state, "--out", out, "--repo", self.root,
                               "--previous-release", self.base], capture_output=True, text=True)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        with open(os.path.join(out, "plan.json"), encoding="utf-8") as fh:
            return json.load(fh), out

    def test_refuses_a_postgres_restart_role_changed_since_the_full_release(self):
        # The fixture's postgres_capacity restarts PostgreSQL, like the real
        # role; head changed its defaults since base, the full release.
        self.run_git("checkout", "-q", "--detach", self.base)
        self.write("deploy/ansible/roles/postgres_capacity/tasks/main.yml",
                   "- name: restart postgresql to activate postmaster settings\n"
                   "  ansible.builtin.service:\n    name: postgresql@16-main.service\n    state: restarted\n")
        self.base = self.commit()
        self.write("deploy/ansible/roles/postgres_capacity/defaults/main.yml", "budget: 89\n")
        self.head = self.commit()
        out = os.path.join(self.root, "refused")
        manifest = os.path.join(self.root, "m.json")
        state = os.path.join(self.root, "h.json")
        with open(manifest, "w", encoding="utf-8") as fh:
            json.dump(dict(RELEASE, base_sha=self.base, git_sha=self.head), fh)
        with open(state, "w", encoding="utf-8") as fh:
            json.dump({"base_git_sha": self.base, "current": self.base, "record": None}, fh)
        proc = subprocess.run([sys.executable, str(HERE / "plan_host_config.py"), "--units-manifest", manifest,
                               "--host-state", state, "--out", out, "--repo", self.root], capture_output=True, text=True)
        self.assertEqual(proc.returncode, 1)
        self.assertIn("postgres_capacity changed since the full release and can restart postgresql", proc.stderr)
        self.assertFalse(os.path.exists(os.path.join(out, "plan.json")))

    def test_gate_mode_writes_gate_json(self):
        report = os.path.join(self.root, "restart.json")
        with open(report, "w", encoding="utf-8") as fh:
            json.dump({"restarted": [{"service": "meterd", "reason": "x", "config_only": False}]}, fh)
        out = os.path.join(self.root, "gate")
        proc = subprocess.run([sys.executable, str(HERE / "plan_host_config.py"), "--gate-from", report, "--out", out],
                              capture_output=True, text=True)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        with open(os.path.join(out, "gate.json"), encoding="utf-8") as fh:
            self.assertEqual(json.load(fh)["gate"][1], ["--unit", "meterd"])

    def test_role_change_since_the_record_converges_only_that_role(self):
        base_hashes = hr.group_hashes(hr.git_tree(self.base, self.root), "control_plane")
        rec = dict(base_hashes, kind="gregale-host-config", commit=self.base, environment="sha256:e")
        got, out = self.plan(rec)
        self.assertEqual((got["action"], got["roles"], got["previous"]), ("converge", ["postgres_capacity"], self.base))
        with open(os.path.join(out, "record.json"), encoding="utf-8") as fh:
            written = json.load(fh)
        self.assertEqual(written["roles"], hr.group_hashes(hr.git_tree(self.head, self.root), "control_plane")["roles"])
        # The release commit's own record converges nothing.
        got, _ = self.plan(dict(written, environment="sha256:e"))
        self.assertEqual(got["action"], "none")


if __name__ == "__main__":
    unittest.main()
