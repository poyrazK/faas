#!/usr/bin/env python3
"""Portable contracts only; these fixtures never certify a live onboarding run."""
import argparse
import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parent))
import onboarding as ob


def passing_report():
    first = {"app_sha256": "a" * 64, "deployment_sha256": "b" * 64, "instance_sha256": "c" * 64,
             "greeting_sha256": "d" * 64, "http_status": 200, "verified": True}
    edited = {**first, "deployment_sha256": "e" * 64, "greeting_sha256": "f" * 64}
    return {"provenance": {"evidence_kind": "deployed", "plan": "free", "cli_version": "v1.2.3", "cli_sha256": "1" * 64},
            "artifact": {"status": "passed", "installer_cli_matches_archive": True, "checksum_verified": True,
                         "release_verify_available": True, "archive_sha256": "2" * 64, "expected_archive_sha256": "2" * 64,
                         "cli_sha256": "1" * 64, "installer_sha256": "3" * 64, "release_tag": "v1.2.3",
                         "cli_source_sha": "4" * 40},
            "phases": {name: {"status": "passed", "seconds": 1} for name in ob.PHASES},
            "proof": {"signed_out_before_login": True, "expected_account_verified": True, "initial_app_count": 0,
                      "final_app_count": 0, "live_app_count": 1, "submitted_releases": 2, "first_response": first,
                      "edited_response": edited, "source_before_sha256": "4" * 64, "source_after_sha256": "5" * 64},
            "metrics": {"install_s": 10, "browser_login_s": 30, "first_response_from_start_s": 120,
                        "edit_to_response_s": 90, "complete_s": 210}}


class EvidenceContracts(unittest.TestCase):
    def test_all_phases_and_pinned_response_evidence_are_required(self):
        self.assertEqual(ob.validate(passing_report()), "passed")
        for phase in ob.PHASES:
            for status in ("not_run", "blocked", "failed", "running"):
                report = passing_report()
                report["phases"][phase]["status"] = status
                self.assertNotEqual(ob.validate(report), "passed")

    def test_fixture_or_missing_evidence_cannot_certify_deployed_onboarding(self):
        for kind in (None, "fixture", "portable", "candidate"):
            report = passing_report()
            report["provenance"]["evidence_kind"] = kind
            self.assertEqual(ob.validate(report), "fixture_only")

    def test_scale_plan_injected_login_extra_resources_and_failed_cleanup_do_not_qualify(self):
        cases = [("provenance", "plan", "scale"), ("proof", "signed_out_before_login", False),
                 ("proof", "expected_account_verified", False), ("proof", "initial_app_count", 1),
                 ("proof", "final_app_count", 1), ("proof", "live_app_count", 2), ("proof", "submitted_releases", 3)]
        for section, key, value in cases:
            with self.subTest(key=key):
                report = passing_report()
                report[section][key] = value
                with self.assertRaises(ob.native.Failure):
                    ob.validate(report)

    def test_installer_binary_and_archive_provenance_cannot_be_missing_or_different(self):
        for key, value in (("cli_sha256", "e" * 64), ("expected_archive_sha256", "f" * 64),
                           ("checksum_verified", False), ("installer_cli_matches_archive", False),
                           ("release_tag", "main"), ("cli_source_sha", None),
                           ("installer_sha256", None), ("release_verify_available", False)):
            with self.subTest(key=key):
                report = passing_report()
                report["artifact"][key] = value
                with self.assertRaises(ob.native.Failure):
                    ob.validate(report)

    def test_replaced_app_same_release_same_greeting_and_unchanged_source_cannot_pass(self):
        for key, value in (("app_sha256", "0" * 64), ("deployment_sha256", "b" * 64), ("greeting_sha256", "d" * 64),
                           ("http_status", 401), ("instance_sha256", ""), ("verified", False)):
            with self.subTest(key=key):
                report = passing_report()
                report["proof"]["edited_response"][key] = value
                with self.assertRaises(ob.native.Failure):
                    ob.validate(report)
        report = passing_report()
        report["proof"]["source_after_sha256"] = report["proof"]["source_before_sha256"]
        with self.assertRaises(ob.native.Failure):
            ob.validate(report)

    def test_missing_nonfinite_and_over_hour_timings_cannot_pass(self):
        for key, value in (("install_s", None), ("browser_login_s", True), ("complete_s", float("nan")),
                           ("complete_s", float("inf")), ("complete_s", 3601), ("edit_to_response_s", -1),
                           ("first_response_from_start_s", 5), ("browser_login_s", 200)):
            with self.subTest(key=key):
                report = passing_report()
                report["metrics"][key] = value
                with self.assertRaises(ob.native.Failure):
                    ob.validate(report)
        for value in (None, True, float("nan"), -1):
            report = passing_report()
            report["phases"]["cleanup_app"]["seconds"] = value
            with self.assertRaises(ob.native.Failure):
                ob.validate(report)

    def test_private_run_requires_same_project_anonymous_denial_and_exact_rollout(self):
        report = passing_report()
        report["provenance"]["template"] = ob.PRIVATE_NODE
        report["artifact"]["private_starter_available"] = True
        self.assertEqual(ob.validate(report), "incomplete")
        report["phases"]["edited_release_rollout"] = {"status": "passed", "seconds": 400}
        proof = report["proof"]
        proof.update(project_absent_before=True, project_absent_after=True,
                     edited_release_rollout={"deployment_sha256": "e" * 64, "traffic_percent": 100})
        for name in ("first_response", "edited_response"):
            proof[name].update(project_sha256="9" * 64, anonymous_http_status=404)
        self.assertEqual(ob.validate(report), "passed")
        for row, key, bad in ((proof, "project_absent_before", False), (proof, "project_absent_after", False),
                              (proof["first_response"], "anonymous_http_status", 200),
                              (proof["edited_response"], "anonymous_http_status", 401),
                              (proof["edited_response"], "project_sha256", "8" * 64),
                              (proof["edited_release_rollout"], "deployment_sha256", "b" * 64),
                              (proof["edited_release_rollout"], "traffic_percent", 99)):
            with self.subTest(key=key), mock.patch.dict(row, {key: bad}), self.assertRaises(ob.native.Failure):
                ob.validate(report)


class ResponseContracts(unittest.TestCase):
    def setUp(self):
        self.session = {"app_id": str(uuid.uuid4()), "deployment_id": str(uuid.uuid4())}
        self.response = {**self.session, "instance_id": str(uuid.uuid4()), "verified": True,
                         "public_access_verified": False, "http_status": 200, "scope": "default",
                         "body": json.dumps({"message": "expected", "node": "v24"})}

    def test_response_is_bound_to_the_exact_app_release_replica_and_greeting(self):
        proof = ob.verify_response(self.response, self.session, "expected")
        self.assertEqual(proof["app_sha256"], ob.fingerprint(self.session["app_id"]))
        self.assertEqual(proof["deployment_sha256"], ob.fingerprint(self.session["deployment_id"]))
        self.assertNotIn("body", proof)
        self.assertNotIn("expected", json.dumps(proof))

    def test_wrong_identity_scope_status_or_truncated_body_cannot_pass(self):
        for key, value in (("app_id", str(uuid.uuid4())), ("deployment_id", str(uuid.uuid4())),
                           ("instance_id", "00000000-0000-0000-0000-000000000000"), ("scope", "staging"),
                           ("project_id", str(uuid.uuid4())), ("verified", False), ("http_status", 401),
                           ("public_access_verified", True), ("body_truncated", True),
                           ("body", json.dumps({"body": {"message": "expected"}})),
                           ("body", "[]"),
                           ("body", json.dumps({"message": "other"})), ("body", "x" * 4097)):
            with self.subTest(key=key), self.assertRaises((ob.native.Failure, ValueError)):
                ob.verify_response({**self.response, key: value}, self.session, "expected")


class IsolationContracts(unittest.TestCase):
    def test_cli_single_pretty_json_and_ndjson_collections_are_both_supported(self):
        account = {"id": "account", "plan": "free"}
        rows = [{"id": "one"}, {"id": "two"}]
        self.assertEqual(ob.documents(json.dumps(account, indent=2)), [account])
        for output in (json.dumps(rows), json.dumps({"items": rows}, indent=2),
                       "\n".join(json.dumps(row) for row in rows)):
            self.assertEqual(ob.documents(output), rows)
        self.assertEqual(ob.documents(""), [])
        with self.assertRaises(ValueError):
            ob.documents('{"id":"partial"')

    def test_child_home_and_authentication_are_clean_without_modifying_parent(self):
        injected = {"FAAS_TOKEN": "SECRET", "FAAS_API": "https://wrong.test", "GREGALE_KEYRING": "off",
                    "XDG_CONFIG_HOME": "/old-config", "CODEX_THREAD_ID": "old-agent", "SSL_CERT_FILE": "/corporate-ca"}
        with tempfile.TemporaryDirectory() as temp, mock.patch.dict(os.environ, injected):
            original = dict(os.environ)
            env = ob.isolated_environment(Path(temp))
            self.assertEqual(dict(os.environ), original)
            self.assertNotIn("FAAS_TOKEN", env)
            self.assertNotIn("FAAS_API", env)
            self.assertNotIn("GREGALE_KEYRING", env)
            self.assertNotIn("CODEX_THREAD_ID", env)
            self.assertEqual(env["SSL_CERT_FILE"], "/corporate-ca")
            self.assertEqual(Path(env["HOME"]), Path(temp) / "home")
            self.assertNotEqual(env["XDG_CONFIG_HOME"], injected["XDG_CONFIG_HOME"])

    def test_terminal_refuses_an_injected_token_for_fresh_login(self):
        with mock.patch.object(ob.native.subprocess, "Popen") as process:
            with self.assertRaises(ob.native.Failure):
                ob.native.Terminal(Path("/unused"), Path("/unused"), {"FAAS_TOKEN": "SECRET"},
                                   arguments=("login",), token_login=False)
            process.assert_not_called()

    def test_terminal_honors_login_arguments_with_a_real_pty(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            executable = root / "gregale"
            executable.write_text("#!" + sys.executable + "\nimport os,sys\n"
                                  "assert os.isatty(0)\nassert sys.argv[1:] == ['--profile','isolated','login']\n"
                                  "assert 'FAAS_TOKEN' not in os.environ\nprint('browser-flow',flush=True)\n")
            executable.chmod(0o700)
            with ob.native.Terminal(executable, root, {}, arguments=("--profile", "isolated", "login"), token_login=False) as terminal:
                terminal.expect("browser-flow", timeout=5)
                terminal.wait(0, timeout=5)


class OwnershipContracts(unittest.TestCase):
    def make_run(self, root, template=ob.NODE):
        return ob.Run(argparse.Namespace(out=root / "evidence", api="https://api.fixture.test", expected_email="private@example.test",
                                         template=template, device_login=False, file_credentials=False))

    def test_missing_app_exit_four_is_accepted_before_start_and_after_cleanup(self):
        # Recorded rc.349 contract: a 404 not_found exits 4, not the generic 1.
        # Exercise call() through a process so a mismatched exit code fails.
        with tempfile.TemporaryDirectory() as temp:
            run = self.make_run(Path(temp))
            run.account = {"id": str(uuid.uuid4()), "plan": "free"}
            run.cli.parent.mkdir()
            run.cli.write_text("#!" + sys.executable + "\nimport json,sys\n"
                               "args=sys.argv[1:]\n"
                               "if args[-2:]==['app','list']:\n print('[]')\n"
                               "elif args[-3:-1]==['app','get']:\n"
                               " print(json.dumps({'status':404,'code':'not_found','exit_code':4}),file=sys.stderr)\n"
                               " sys.exit(4)\n"
                               "else:\n sys.exit(99)\n")
            run.cli.chmod(0o700)
            run.preflight()
            self.assertEqual(run.report["proof"]["initial_app_count"], 0)
            run.registered = True
            self.assertEqual(run.cleanup(), [])
            self.assertEqual(run.report["phases"]["cleanup_app"]["status"], "passed")
            self.assertEqual(run.report["proof"]["final_app_count"], 0)

    def test_free_plan_gate_does_not_downgrade_or_mutate_another_plan(self):
        with tempfile.TemporaryDirectory() as temp:
            run = self.make_run(Path(temp))
            run.account = {"id": str(uuid.uuid4()), "plan": "scale"}
            with mock.patch.object(run, "call") as call:
                with self.assertRaises(ob.Blocked):
                    run.preflight()
                call.assert_not_called()
                self.assertFalse(run.registered)

    def test_existing_free_app_is_never_reused_or_deleted(self):
        with tempfile.TemporaryDirectory() as temp:
            run = self.make_run(Path(temp))
            run.account = {"id": str(uuid.uuid4()), "plan": "free"}
            with mock.patch.object(run, "call", return_value=[{"id": str(uuid.uuid4()), "slug": "existing"}]) as call:
                with self.assertRaises(ob.Blocked):
                    run.preflight()
                self.assertEqual(run.cleanup(), [])
                self.assertEqual(call.call_count, 1)

    def test_cleanup_refuses_an_app_whose_identity_changed(self):
        with tempfile.TemporaryDirectory() as temp:
            run = self.make_run(Path(temp))
            run.registered = True
            run.session = {"app_id": str(uuid.uuid4())}
            with mock.patch.object(run, "call", return_value=[{"slug": run.slug, "id": str(uuid.uuid4())}]) as call:
                self.assertEqual(run.cleanup(), ["app_cleanup_failed"])
                self.assertEqual(call.call_args_list, [mock.call("app", "list")])

    def test_private_cleanup_refuses_a_replaced_project(self):
        with tempfile.TemporaryDirectory() as temp:
            run = self.make_run(Path(temp), ob.PRIVATE_NODE)
            run.registered = True
            run.session = {"app_id": str(uuid.uuid4()), "project_id": str(uuid.uuid4())}
            app = {"slug": run.slug, "id": run.session["app_id"], "project_id": run.session["project_id"]}
            project = {"slug": run.slug, "id": str(uuid.uuid4())}
            with mock.patch.object(run, "call", side_effect=[[app], [project]]) as call:
                self.assertEqual(run.cleanup(), ["app_cleanup_failed"])
                self.assertEqual(call.call_args_list, [mock.call("app", "list"), mock.call("project", "list")])

    def test_reports_exclude_supplied_email_login_url_response_and_local_paths(self):
        with tempfile.TemporaryDirectory() as temp:
            run = self.make_run(Path(temp))
            run.report["proof"]["first_response"] = ob.verify_response(
                {"app_id": "a" * 32, "deployment_id": "b" * 32, "instance_id": "c" * 32,
                 "verified": True, "public_access_verified": False, "http_status": 200,
                 "scope": "default", "body": json.dumps({"message": "PRIVATE"})},
                {"app_id": "a" * 32, "deployment_id": "b" * 32}, "PRIVATE")
            run.checkpoint()
            rendered = (run.out / "onboarding.json").read_text() + (run.out / "onboarding.md").read_text()
            for value in ("private@example.test", "PRIVATE", str(run.private), "device_code", "SECRET"):
                self.assertNotIn(value, rendered)


class EntryPointContracts(unittest.TestCase):
    def prepared_run(self, root):
        return ob.Run(argparse.Namespace(out=root / "evidence", api="https://api.fixture.test", expected_email="private@example.test",
                                         template=ob.NODE, device_login=False, file_credentials=False))

    def test_install_only_never_logs_in_or_deploys_and_cannot_pass(self):
        with tempfile.TemporaryDirectory() as temp:
            run = self.prepared_run(Path(temp))
            argv = ["onboarding.py", "--api", "https://api.fixture.test", "--install-only", "--out", str(run.out)]
            with mock.patch.object(sys, "argv", argv), mock.patch.object(ob, "Run", return_value=run), \
                    mock.patch.object(run, "install"), mock.patch.object(run, "call"), \
                    mock.patch.object(run, "login") as login, mock.patch.object(run, "start_and_edit") as start, \
                    mock.patch.object(run, "cleanup", return_value=[]), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(ob.main(), 1)
            login.assert_not_called()
            start.assert_not_called()
            self.assertEqual(run.report["status"], "blocked")
            self.assertEqual(run.report["provenance"]["evidence_kind"], "installer_only")

    def test_cleanup_failure_overrides_a_blocked_browser_step(self):
        with tempfile.TemporaryDirectory() as temp:
            run = self.prepared_run(Path(temp))
            argv = ["onboarding.py", "--api", "https://api.fixture.test", "--expected-email", "private@example.test", "--out", str(run.out)]
            with mock.patch.object(sys, "argv", argv), mock.patch.object(ob, "Run", return_value=run), \
                    mock.patch.object(run, "install"), mock.patch.object(run, "login", side_effect=ob.Blocked("browser_waiting")), \
                    mock.patch.object(run, "cleanup", return_value=["isolated_login_cleanup_failed"]) as cleanup, \
                    contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(ob.main(), 1)
            cleanup.assert_called_once()
            self.assertEqual(run.report["status"], "failed")
            self.assertEqual(run.report["reason"], "isolated_login_cleanup_failed")


if __name__ == "__main__":
    unittest.main()
