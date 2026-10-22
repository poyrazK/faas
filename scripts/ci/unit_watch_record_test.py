#!/usr/bin/env python3
"""Unit tests for unit_watch_record.py, the control plane's record of unit
releases not yet watched clean
(docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md)."""
import contextlib
import importlib.util
import io
import json
import os
import pathlib
import shutil
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("unit_watch_record", HERE / "unit_watch_record.py")
uwr = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = uwr
spec.loader.exec_module(uwr)

BASE, BASE2 = "b" * 40, "9" * 40
G, A, B, C = "0" * 39 + "1", "a" * 40, "c" * 40, "d" * 40
NODE_TARGET = {"node": "fsn-2", "ssh_host": "10.0.0.2", "ssh_user": "root", "ssh_port": "22",
               "ssh_host_key_sha256": "SHA256:" + "x" * 43}


def plan(release, expect, only=("apid",), verify=None, gate=None):
    return {"action": "deploy" if release != expect else "converge", "release_id": release, "expect_current": expect,
            "only": list(only), "verify": verify or [f"{s}=http://127.0.0.1:1/readyz" for s in only],
            "gate": gate or [["--unit", s] for s in only]}


def opened(record, host, p, live, now, owner="10", **kw):
    return uwr.open_host(record, owner=owner, host=host, plan=p, live=live, base=kw.pop("base", BASE), now=now, **kw)


class RecordTest(unittest.TestCase):
    def test_first_deploy_records_the_release_it_replaced_as_known_good(self):
        r = opened(None, "control-plane", plan(A, G), G, 100, commit="1" * 40)
        e = r["hosts"]["control-plane"]
        self.assertEqual((e["known_good"], e["current"], e["releases"]), (G, A, [A]))
        self.assertEqual((r["owner_run"], r["opened_at"], r["order"]), ("10", 100, ["control-plane"]))
        self.assertEqual(e["commits"], ["1" * 40])
        self.assertIsNone(e["target"])

    def test_a_newer_run_keeps_known_good_and_takes_ownership(self):
        r = opened(None, "control-plane", plan(A, G, only=("apid",)), G, 100)
        r = opened(r, "control-plane", plan(B, A, only=("meterd",)), A, 400, owner="11", commit="2" * 40)
        e = r["hosts"]["control-plane"]
        self.assertEqual((e["known_good"], e["current"], e["releases"]), (G, B, [A, B]))
        self.assertEqual(e["services"], ["apid", "meterd"])
        self.assertEqual(e["gate"], [["--unit", "apid"], ["--unit", "meterd"]])
        self.assertEqual((e["first_activation"], e["last_activation"]), (100, 400))
        self.assertEqual(r["owner_run"], "11")

    def test_at_most_max_unwatched_releases_per_host(self):
        r = opened(None, "control-plane", plan(A, G), G, 100)
        r = opened(r, "control-plane", plan(B, A), A, 200)
        with self.assertRaisesRegex(uwr.RecordError, "already holds 2 unwatched releases"):
            opened(r, "control-plane", plan(C, B), B, 300)
        # A converge rerun of the current release adds nothing and passes.
        r = opened(r, "control-plane", plan(B, B), B, 300)
        self.assertEqual(r["hosts"]["control-plane"]["releases"], [A, B])

    def test_converge_without_unwatched_releases_records_nothing(self):
        self.assertIsNone(opened(None, "control-plane", plan(G, G), G, 100))

    def test_a_host_changed_under_the_record_starts_over(self):
        r = opened(None, "control-plane", plan(A, G), G, 100)
        # A full release moved the base: known-good from the old base is void.
        r2 = opened(json.loads(json.dumps(r)), "control-plane", plan(B, C), C, 200, base=BASE2)
        self.assertEqual((r2["hosts"]["control-plane"]["known_good"], r2["hosts"]["control-plane"]["releases"]), (C, [B]))
        # A manual deploy or rollback changed the active release.
        r3 = opened(json.loads(json.dumps(r)), "control-plane", plan(B, C), C, 200)
        self.assertEqual(r3["hosts"]["control-plane"]["known_good"], C)

    def test_hosts_keep_their_first_activation_order_and_node_targets(self):
        r = opened(None, "fsn-2", plan(A, G, only=("gatewayd-internal",)), G, 100, target=NODE_TARGET)
        r = opened(r, "control-plane", plan(B, C, only=("gatewayd-public",)), C, 200)
        self.assertEqual(r["order"], ["fsn-2", "control-plane"])
        self.assertEqual(r["hosts"]["fsn-2"]["target"], NODE_TARGET)

    def test_revert_forgets_a_failed_deploy(self):
        r = opened(None, "control-plane", plan(A, G), G, 100)
        r = opened(r, "control-plane", plan(B, A), A, 200)
        r = uwr.revert_host(r, host="control-plane", live=A)
        self.assertEqual((r["hosts"]["control-plane"]["releases"], r["hosts"]["control-plane"]["current"]), ([A], A))
        self.assertIsNone(uwr.revert_host(r, host="control-plane", live=G))

    def test_revert_to_an_unknown_release_leaves_the_entry_stale(self):
        r = opened(None, "control-plane", plan(A, G), G, 100)
        r = uwr.revert_host(r, host="control-plane", live=C)
        self.assertEqual(r["hosts"]["control-plane"]["current"], A)  # differs from the host: left alone

    def test_bad_values_are_refused(self):
        for kwargs in ({"live": "x"}, {"host": "../etc"}, {"owner": "1; rm"}):
            with self.subTest(**kwargs):
                args = {"owner": "10", "host": "control-plane", "plan": plan(A, G), "live": G, "base": BASE, "now": 1}
                args.update(kwargs)
                with self.assertRaises(uwr.RecordError):
                    uwr.open_host(None, **args)


class CommandTest(unittest.TestCase):
    def setUp(self):
        d = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, d)
        self.dir = pathlib.Path(d)
        self.record = str(self.dir / "deploy" / "unit-watch.json")

    def cli(self, *args):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = uwr.main([args[0], "--record", self.record, *args[1:]])
        return code, json.loads(out.getvalue() or "null"), err.getvalue()

    def plan_file(self, p):
        path = self.dir / f"plan-{p['release_id'][:4]}.json"
        path.write_text(json.dumps(p))
        return str(path)

    def test_open_show_close(self):
        code, rec, _ = self.cli("open", "--owner", "10", "--host", "control-plane", "--plan", self.plan_file(plan(A, G)),
                                "--live", G, "--base", BASE, "--now", "100", "--commit", "1" * 40)
        self.assertEqual(code, 0)
        self.assertEqual(rec["hosts"]["control-plane"]["releases"], [A])
        self.assertEqual(os.stat(self.record).st_mode & 0o777, 0o600)
        self.assertEqual(self.cli("show")[1]["owner_run"], "10")
        code, _, err = self.cli("close", "--owner", "11")
        self.assertEqual(code, uwr.NOT_OWNER)
        self.assertIn("run 10 owns the record", err)
        code, rec, _ = self.cli("close", "--owner", "10")
        self.assertEqual((code, rec), (0, None))
        self.assertFalse(os.path.exists(self.record))
        self.assertEqual(self.cli("show"), (0, None, ""))

    def test_open_at_the_cap_exits_3_and_keeps_the_record(self):
        for release, expect in ((A, G), (B, A)):
            self.cli("open", "--owner", "10", "--host", "control-plane", "--plan", self.plan_file(plan(release, expect)),
                     "--live", expect, "--base", BASE, "--now", "100")
        code, rec, err = self.cli("open", "--owner", "12", "--host", "control-plane", "--plan", self.plan_file(plan(C, B)),
                                  "--live", B, "--base", BASE, "--now", "300")
        self.assertEqual(code, uwr.CAP_REACHED)
        self.assertIn("wait for the watch to finish", err)
        self.assertEqual(rec["owner_run"], "10")

    def test_revert_command(self):
        target = self.dir / "target.json"
        target.write_text(json.dumps(NODE_TARGET))
        _, rec, _ = self.cli("open", "--owner", "10", "--host", "fsn-2", "--plan", self.plan_file(plan(A, G)), "--live", G,
                             "--base", BASE, "--now", "100", "--target-file", str(target))
        self.assertEqual(rec["hosts"]["fsn-2"]["target"], NODE_TARGET)
        code, rec, _ = self.cli("revert", "--host", "fsn-2", "--live", G)
        self.assertEqual((code, rec), (0, None))

    def test_adopt_hands_an_orphaned_record_to_a_new_watch(self):
        self.cli("open", "--owner", "10", "--host", "control-plane", "--plan", self.plan_file(plan(A, G)),
                 "--live", G, "--base", BASE, "--now", "100")
        code, rec, _ = self.cli("adopt", "--owner", "20", "--now", "5000")
        self.assertEqual(code, 0)
        self.assertEqual((rec["owner_run"], rec["watch_from"]), ("20", 5000))
        self.assertEqual(rec["hosts"]["control-plane"]["known_good"], G)
        os.remove(self.record)
        self.assertEqual(self.cli("adopt", "--owner", "21", "--now", "6000")[:2], (0, None))

    def test_a_foreign_file_is_an_error(self):
        os.makedirs(os.path.dirname(self.record), exist_ok=True)
        pathlib.Path(self.record).write_text('{"kind": "something-else"}')
        self.assertEqual(self.cli("show")[0], 2)


if __name__ == "__main__":
    unittest.main()
