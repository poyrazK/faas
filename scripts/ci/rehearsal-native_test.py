#!/usr/bin/env python3
"""Portable runner checks; none of these qualify KVM or the deployed platform."""
import importlib.util
from contextlib import ExitStack
import json
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import MagicMock, patch

spec = importlib.util.spec_from_file_location("native", Path(__file__).with_name("rehearsal-native.py"))
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)
REPO = Path(__file__).resolve().parents[2]


class RehearsalNativeTests(unittest.TestCase):
    def test_postgres_journey_pins_the_scoped_fault_recipe(self):
        with tempfile.TemporaryDirectory() as root:
            destination = Path(root) / "fixture"
            native.prepare_fixture(REPO, destination, "postgres-recovery")
            recipe = (destination / "gregale-test.yaml").read_text()
            self.assertIn("postgres_fault:", recipe)
            self.assertIn('POSTGRES_RECOVERY_FIXTURE: "1"', recipe)
            self.assertIn("base_seconds: 3", recipe)
            self.assertNotIn("suspend_resume:", recipe)
            self.assertEqual(native.git(destination, "status", "--porcelain"), "")

    def test_preparation_pins_two_revisions_and_changes_only_retry_behavior(self):
        with tempfile.TemporaryDirectory() as root:
            destination = Path(root) / "fixture"
            with patch.object(native, "execute") as execution:
                pins = native.prepare_fixture(REPO, destination)
            execution.assert_not_called()
            self.assertNotEqual(pins["baseline"], pins["candidate"])
            self.assertEqual(native.git(destination, "diff", "--name-only", pins["baseline"], pins["candidate"]), "worker/revision.json")
            self.assertEqual(set(native.git(destination, "ls-files").splitlines()), set(native.FILES))
            for revision, duplicate in ((pins["baseline"], False), (pins["candidate"], True)):
                value = json.loads(native.git(destination, "show", revision + ":worker/revision.json"))
                self.assertEqual(value, {"duplicate_notification": duplicate})
            self.assertEqual(native.git(destination, "status", "--porcelain"), "")
            with self.assertRaises(FileExistsError):
                native.prepare_fixture(REPO, destination)

    def test_prepare_only_never_claims_native_qualification(self):
        with tempfile.TemporaryDirectory() as root:
            out = Path(root) / "evidence"
            with patch.object(sys, "argv", ["runner", "--repo", str(REPO), "--out", str(out), "--prepare-only"]), \
                    patch.object(native, "native_run") as run:
                native.main()
            run.assert_not_called()
            self.assertEqual(json.loads((out / "preparation.json").read_text())["native_qualified"], False)
            self.assertFalse((out / "qualification.json").exists())
            self.assertEqual(out.stat().st_mode & 0o777, 0o700)

    def test_native_execution_requires_explicit_opt_in_before_api_access(self):
        with patch.dict(native.os.environ, {}, clear=True), patch.object(native, "api_get") as api:
            with self.assertRaisesRegex(ValueError, "GREGALE_REHEARSAL_NATIVE_ACCEPTANCE"):
                native.runner_configuration(REPO)
            api.assert_not_called()

    def test_native_execution_refuses_missing_kvm(self):
        with patch.dict(native.os.environ, {"GREGALE_REHEARSAL_NATIVE_ACCEPTANCE": "1"}), \
                patch.object(native.platform, "system", return_value="Linux"), \
                patch.object(native.platform, "machine", return_value="x86_64"), \
                patch.object(native.os, "geteuid", return_value=0), \
                patch.object(native.Path, "is_char_device", return_value=False), \
                patch.object(native, "api_get") as api:
            with self.assertRaisesRegex(ValueError, "native KVM"):
                native.runner_configuration(REPO)
            api.assert_not_called()

    def test_host_release_api_and_account_must_match(self):
        account = "11111111-1111-4111-8111-111111111111"
        marker = {"dedicated_native_host": True, "api_origin": "https://acceptance.example.test", "account_id": account}
        for mode in ("valid", "different release", "dirty checkout", "wrong API", "wrong token"):
            with self.subTest(mode=mode), ExitStack() as stack:
                stack.enter_context(patch.dict(native.os.environ, {"GREGALE_REHEARSAL_NATIVE_ACCEPTANCE": "1", "FAAS_API": "https://acceptance.example.test", "FAAS_TOKEN": "test-token", "FAAS_TEST_SKIP_POSTGRES": "0", "FAAS_TEST_FAKE_STORAGE": "0"}))
                for target, attribute, value in ((native.platform, "system", "Linux"), (native.platform, "machine", "x86_64"),
                                                  (native.os, "geteuid", 0), (native.os, "access", True),
                                                  (native.Path, "is_char_device", True), (native.shutil, "which", "/tool")):
                    stack.enter_context(patch.object(target, attribute, return_value=value))
                enrollment = dict(marker)
                if mode == "wrong API":
                    enrollment["api_origin"] = "https://other.example.test"
                stack.enter_context(patch.object(native, "root_file", side_effect=[json.dumps(enrollment), "a" * 40]))
                stack.enter_context(patch.object(native, "git", side_effect=["b" * 40 if mode == "different release" else "a" * 40, "M source.go" if mode == "dirty checkout" else ""]))
                stack.enter_context(patch.object(native, "api_get", return_value=(200, {"id": "foreign" if mode == "wrong token" else account})))
                if mode == "valid":
                    self.assertEqual(native.runner_configuration(REPO)["account_id"], account)
                else:
                    with self.assertRaises(ValueError):
                        native.runner_configuration(REPO)

    def test_cleanup_observation_requires_actual_workload_deletion(self):
        with tempfile.TemporaryDirectory() as root:
            reports = []
            for index in range(2):
                path = Path(root) / (str(index) + ".json")
                native.write_json(path, {side: [{"app_slug": side + str(index), "services": {"worker": side + "worker" + str(index)}}]
                                         for side in ("baseline_runs", "candidate_runs")})
                reports.append(path)
            with patch.object(native, "api_get", return_value=(404, None)):
                self.assertEqual(len(native.verify_app_cleanup("https://acceptance.example.test", reports)), 8)
            for status in (200, 401, 403, 500):
                with self.subTest(status=status), patch.object(native, "api_get", return_value=(status, None)):
                    with self.assertRaisesRegex(ValueError, "deletion"):
                        native.verify_app_cleanup("https://acceptance.example.test", reports)

    def test_timeout_interrupts_and_joins_only_the_owned_process_group(self):
        child = MagicMock(pid=12345)
        child.wait.side_effect = [subprocess.TimeoutExpired("fixture", 1), 0]
        child.poll.return_value = None
        with tempfile.TemporaryDirectory() as root, patch.object(native.subprocess, "Popen", return_value=child), \
                patch.object(native.os, "killpg") as kill:
            with self.assertRaises(subprocess.TimeoutExpired):
                native.execute(["coordinator"], REPO, Path(root) / "run.log", 1)
            kill.assert_called_once_with(child.pid, signal.SIGINT)
            self.assertEqual(child.wait.call_count, 2)

    def test_failure_still_runs_leakcheck_and_never_publishes_pass(self):
        identity = {"platform_commit": "a" * 40, "api_origin": "https://acceptance.example.test", "account_id": "account", "native_runner": True}
        with tempfile.TemporaryDirectory() as root:
            out = Path(root)
            calls = []

            def execute(args, _repo, log, _timeout, expected=0):
                calls.append(log.name)
                if log.name == "build-cli.log":
                    raise ValueError("build failed")

            with patch.object(native, "runner_configuration", return_value=identity), \
                    patch.object(native, "NATIVE_LOCK", out / "lock"), \
                    patch.object(native, "execute", side_effect=execute):
                with self.assertRaisesRegex(ValueError, "build failed"):
                    native.native_run(REPO, out)
            self.assertEqual(calls, ["leak-before.log", "build-cli.log", "leak-after.log"])
            self.assertEqual(json.loads((out / "qualification.json").read_text())["status"], "failed")


if __name__ == "__main__":
    unittest.main()
