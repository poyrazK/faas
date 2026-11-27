#!/usr/bin/env python3
"""Portable runner checks; none of these qualify KVM or the deployed platform."""
import importlib.util
from contextlib import ExitStack
from copy import deepcopy
import io
import json
from pathlib import Path
import signal
import subprocess
import sys
import tarfile
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


class HostedTaskRehearsalTests(unittest.TestCase):
    def prepare(self, out):
        with patch.object(sys, "argv", ["runner", "--repo", str(REPO), "--out", str(out),
                                        "--journey", "hosted-task", "--prepare-only"]), \
                patch.object(native, "native_run") as run, patch.object(native, "api_get") as api:
            native.main()
        run.assert_not_called()
        api.assert_not_called()
        self.assertFalse((out / "qualification.json").exists())
        receipt = json.loads((out / "preparation.json").read_text())
        self.assertIs(receipt["native_qualified"], False)
        self.assertEqual(receipt["journey"], "hosted-task")
        return receipt

    def test_fixture_archives_are_reproducible_and_keep_separate_roles(self):
        with tempfile.TemporaryDirectory() as root:
            first, second = (Path(root) / name for name in ("first", "second"))
            self.assertEqual(self.prepare(first), self.prepare(second))
            contents = {}
            for role in ("web", "worker"):
                path = Path("fixture") / (role + ".tar.gz")
                self.assertEqual((first / path).read_bytes(), (second / path).read_bytes())
                with tarfile.open(first / path, "r:gz") as bundle:
                    self.assertTrue(all(entry.isfile() and "/" not in entry.name for entry in bundle))
                    contents[role] = {entry.name: bundle.extractfile(entry).read() for entry in bundle.getmembers()}
                config = json.loads(contents[role]["gregale-mcp.json"])
                self.assertTrue(config["tasks"]["enabled"])
                self.assertEqual(config["tasks"]["namespace_env"], "MCP_TASK_NAMESPACE")
                starter = REPO / "cmd/gregale/templates/mcp-node-tasks"
                for name in ("package-lock.json", "tasks-migrate.js", "task-store.js"):
                    self.assertEqual(contents[role][name], (starter / name).read_bytes())
            self.assertEqual(set(contents["worker"]) - set(contents["web"]), {"rehearsal-claim.js"})
            self.assertEqual(set(contents["web"]) - set(contents["worker"]), set())
            worker_source = contents["worker"]["tasks-worker.js"]
            stock_worker = (starter / "tasks-worker.js").read_bytes()
            self.assertEqual(contents["web"]["tasks-worker.js"], stock_worker)
            self.assertEqual(
                worker_source,
                b"import { createObservedStore } from './rehearsal-claim.js';\n" +
                stock_worker.replace(
                    b"startMcpTaskRuntime(config, { role: 'worker' })",
                    b"startMcpTaskRuntime(config, { role: 'worker', createStore: createObservedStore })",
                ),
            )
            self.assertIn(b"lease_sha256: createHash", contents["worker"]["rehearsal-claim.js"])
            self.assertEqual({name for name in contents["web"] if contents["web"][name] != contents["worker"][name]},
                             {"package.json", "gregale.yaml", "tasks-worker.js"})
            self.assertEqual(json.loads(contents["web"]["package.json"])["scripts"]["start"],
                             "MCP_TASKS_ROLE=web node server.js")
            self.assertIn(b"start: npm run start:tasks-worker\nworker:", contents["worker"]["gregale.yaml"])

    def test_live_hosted_task_launch_stops_before_allocating_resources(self):
        with tempfile.TemporaryDirectory() as root:
            out = Path(root) / "evidence"
            with patch.object(sys, "argv", ["runner", "--out", str(out), "--journey", "hosted-task"]), \
                    patch.object(native, "native_run") as run:
                with self.assertRaisesRegex(ValueError, "approved source intent and plan digest"):
                    native.main()
            run.assert_not_called()
            self.assertFalse(out.exists())

    def test_native_receipt_publication_requires_a_clean_host_after_execution(self):
        identity = {"platform_commit": "a" * 40, "api_origin": "https://acceptance.example.test",
                    "account_id": "account", "node_id": "node", "native_runner": True}
        approval = ("b" * 64, "c" * 64)
        for leak in (False, True):
            with self.subTest(leak=leak), tempfile.TemporaryDirectory() as root:
                out = Path(root)
                pending = out / "hosted-task-receipt.pending"
                checks = []

                def run(_repo, directory, intent, plan):
                    self.assertEqual((directory, intent, plan), (out, *approval))
                    pending.write_text("{}")
                    return pending

                def execute(_args, _repo, log, _timeout):
                    checks.append(log.name)
                    if leak and log.name == "leak-after.log":
                        raise ValueError("native leak")

                with patch.object(native, "runner_configuration", return_value=identity), \
                        patch.object(native, "NATIVE_LOCK", out / "lock"), \
                        patch.object(native, "run_hosted_task", side_effect=run), \
                        patch.object(native, "execute", side_effect=execute), patch.object(sys, "stdout"):
                    if leak:
                        with self.assertRaisesRegex(ValueError, "native leak"):
                            native.native_run(REPO, out, "hosted-task", approval)
                    else:
                        native.native_run(REPO, out, "hosted-task", approval)
                self.assertEqual(checks, ["leak-before.log", "leak-after.log"])
                self.assertEqual((out / "hosted-task-receipt.json").exists(), not leak)
                journal = json.loads((out / "qualification.json").read_text())
                self.assertEqual(journal["status"], "failed" if leak else "passed")

    def test_hosted_task_child_receives_prepared_pins_and_private_output(self):
        with tempfile.TemporaryDirectory() as root:
            out = Path(root)
            build, child = [], []

            def execute(args, _repo, log, _timeout, *, receipt=None):
                if receipt is None:
                    build.extend(args)
                    args[args.index("-o") + 1].touch()
                else:
                    child.extend(args)
                    receipt.write_text("{}")

            with patch.object(native, "execute", side_effect=execute), \
                    patch.object(native, "HOSTED_TASK_BUILD_ROOT", out), \
                    patch.object(native.rehearsal_hosted_task, "verify") as verify:
                pending = native.run_hosted_task(REPO, out, "a" * 64, "b" * 64)
            verify.assert_called_once_with(out, pending)
            self.assertIn("hosted_task_acceptance", build)
            self.assertEqual(pending, out / "hosted-task-receipt.pending")
            self.assertFalse((out / "hosted-task-receipt.json").exists())
            prepared = json.loads((out / "preparation.json").read_text())
            for role in ("web", "worker"):
                self.assertEqual(child[child.index("--" + role + "-source") + 1], prepared["source_sha256"][role])
                self.assertEqual(child[child.index("--" + role + "-archive") + 1], prepared["archive_sha256"][role])

    def test_verifier_binds_the_same_task_and_rejects_incomplete_evidence(self):
        with tempfile.TemporaryDirectory() as root:
            out = Path(root) / "evidence"
            prepared = self.prepare(out)
            run_id = "aa965f99-7ce3-4ec3-867a-9db2d52852b0"
            task_id = "079706c8-e14e-4409-9c76-683a5af5d577"
            deployment_id = "6bf21bb3-c5c1-4587-9554-92bf5682fb17"
            old_instance = "e4372bd9-f502-41ed-9118-b3b5b72f9243"
            new_instance = "dd511fbc-f133-44e1-b068-57f094211f5a"
            running = {"task_id": task_id, "status": "running", "deployment_id": deployment_id}
            receipt = {
                "version": 1, "archive_sha256": prepared["archive_sha256"], "run_id": run_id, "task_id": task_id,
                "initial": {**running, "attempt": 1, "instance_id": old_instance},
                "stopped_instance_id": old_instance,
                "recovered": {**running, "attempt": 2, "instance_id": new_instance},
                "result": {"task_id": task_id, "status": "completed",
                           "text": "Report hosted-task-rehearsal is ready after 20 steps."},
                "observer": {"namespace": "rehearsal-" + run_id, "task_id": task_id, "attempt": 2,
                             "task_rows": 1, "completed_result_rows": 1},
                "cleanup": {"workloads_remaining": 0, "database_present": False, "logins_remaining": 0,
                            "secrets_remaining": 0, "sources_remaining": 0},
            }
            path = out / "hosted-task-receipt.json"
            native.write_json(path, receipt)
            with patch.object(sys, "argv", ["runner", "--verify-hosted-task", str(out)]), \
                    patch.object(sys, "stdout", new_callable=io.StringIO) as stdout, \
                    patch.object(native, "api_get") as api:
                native.main()
            api.assert_not_called()
            self.assertEqual(json.loads(stdout.getvalue()), {
                "journey": "hosted-task", "receipt_verified": True, "native_qualified": False,
            })
            for field, key, value in (
                ("archive_sha256", "web", "0" * 64),
                ("initial", "attempt", True),
                ("recovered", "attempt", 1),
                ("recovered", "instance_id", old_instance),
                ("recovered", "deployment_id", run_id),
                ("result", "task_id", run_id),
                ("result", "text", "wrong fixture result"),
                ("observer", "namespace", "other-namespace"),
                ("observer", "completed_result_rows", 2),
                ("cleanup", "workloads_remaining", 1),
                ("cleanup", "database_present", True),
                ("cleanup", "sources_remaining", 1),
            ):
                with self.subTest(field=field, key=key, value=value):
                    changed = deepcopy(receipt)
                    changed[field][key] = value
                    native.write_json(path, changed)
                    with self.assertRaises(ValueError):
                        native.rehearsal_hosted_task.verify(out)

            incomplete = deepcopy(receipt)
            del incomplete["stopped_instance_id"]
            native.write_json(path, incomplete)
            with self.assertRaisesRegex(ValueError, "unexpected hosted Task receipt fields"):
                native.rehearsal_hosted_task.verify(out)
            path.write_text('{"version":1,"version":1}')
            with self.assertRaisesRegex(ValueError, "duplicate evidence field"):
                native.rehearsal_hosted_task.verify(out)
            path.write_bytes(b" " * (64 * 1024 + 1))
            with self.assertRaisesRegex(ValueError, "bounded regular evidence file"):
                native.rehearsal_hosted_task.verify(out)
            native.write_json(path, receipt)
            (out / "fixture/worker.tar.gz").write_bytes(b"replaced archive")
            with self.assertRaisesRegex(ValueError, "archive digests disagree"):
                native.rehearsal_hosted_task.verify(out)


if __name__ == "__main__":
    unittest.main()
