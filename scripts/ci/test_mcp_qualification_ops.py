import importlib.util
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest


REPO = Path(__file__).resolve().parents[2]
RUNNER = REPO / "scripts" / "ops" / "mcp-qualification.py"


def scale_zero_receipt(target):
    return {
        "version": 2,
        "runner": "gregale-mcp-tasks-scale-zero-v2",
        "check": "tasks_scale_from_zero",
        "status": "passed",
        "target": target,
        "app": "test-staging",
        "observer_app": "test-observer",
        "observed_at": "2026-10-10T12:00:00+00:00",
        "checks": {
            "observer_app_linked": "passed",
            "observer_app_ready_before": "passed",
            "observer_app_ready_throughout": "passed",
            "observer_heartbeat_fresh_before": "passed",
            "observer_heartbeat_fresh_throughout": "passed",
            "scale_to_zero_policy": "passed",
            "zero_workers_before": "passed",
            "fresh_zero_metric_before": "passed",
            "backlog_metric_observed": "passed",
            "worker_scaled_up": "passed",
            "backlog_and_worker_observed_together": "passed",
            "task_completed": "passed",
            "fresh_zero_metric_after": "passed",
            "workers_returned_to_zero": "passed",
        },
        "observations": {
            "initial_replicas": 0,
            "peak_replicas": 1,
            "peak_outstanding": 1,
            "final_replicas": 0,
            "freshness_seconds": 300,
            "completed_tasks": 1,
            "observer_min_replicas": 1,
            "observer_initial_replicas": 1,
        },
        "failures": [],
    }


class NativeQualificationProbeTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.evidence = self.root / "evidence"
        self.commit = "a" * 40
        initialized = subprocess.run(
            [sys.executable, str(RUNNER), "init", "--dir", str(self.evidence), "--commit", self.commit],
            check=False, capture_output=True, text=True,
        )
        if initialized.returncode != 0:
            self.fail(initialized.stdout + initialized.stderr)
        self.fake_gregale = self.root / "gregale"
        self.fake_gregale.write_text(
            "#!/usr/bin/env python3\n"
            "import json, os, sys\n"
            "if sys.argv[1] == 'park':\n"
            "    sys.exit(0)\n"
            "if sys.argv[1] == 'ps':\n"
            "    if os.environ.get('FAKE_ACTIVE_INSTANCE'):\n"
            "        print(json.dumps({'id': 'opaque', 'state': 'running'}))\n"
            "    sys.exit(0)\n"
            "if sys.argv[1:3] != ['mcp', 'doctor']:\n"
            "    sys.exit(2)\n"
            "tier = os.environ.get('FAKE_WAKE_TIER', 'cold')\n"
            "print(json.dumps({'ok': True, 'checks': [\n"
            "  {'name': 'modern_catalog_discovery', 'status': 'passed'},\n"
            "  {'name': 'untrusted_origin', 'status': 'passed'},\n"
            "  {'name': 'live_progress', 'status': 'passed'}],\n"
            "  'discovery': {'wake_tier': tier},\n"
            "  'stream': {'progress_ms': [10, 150], 'duration_ms': 300,\n"
            "    'result': {'access_token': 'do-not-store-token'}}}))\n"
        )
        self.fake_gregale.chmod(0o700)

    def probe(self, name="native_cold_boot", app="test-staging", confirmation=None, extra_env=None):
        command = [
            sys.executable, str(RUNNER), "probe", "--dir", str(self.evidence),
            "--name", name, "--app", app, "--confirm-staging-app", confirmation or app,
            "--target", "native x86_64 KVM host/build / test app", "--stream-tool", "stream_demo",
            "--gregale", str(self.fake_gregale),
        ]
        env = os.environ.copy()
        if extra_env:
            env.update(extra_env)
        return subprocess.run(command, check=False, capture_output=True, text=True, env=env)

    def test_probe_records_redacted_cold_boot_receipt(self):
        result = self.probe()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        manifest = json.loads((self.evidence / "manifest.json").read_text())
        row = next(row for row in manifest["checks"] if row["name"] == "native_cold_boot")
        self.assertEqual(row["status"], "passed")
        self.assertEqual(row["evidence_type"], "gregale-mcp-native-v1")
        receipt_path = self.evidence / row["artifact"]
        receipt = json.loads(receipt_path.read_text())
        self.assertEqual(receipt["observed_wake_tier"], "cold")
        self.assertTrue(receipt["doctor_ok"])
        self.assertEqual(stat.S_IMODE(receipt_path.stat().st_mode), 0o600)
        self.assertNotIn("access_token", receipt_path.read_text())
        self.assertNotIn("do-not-store-token", receipt_path.read_text())

        generic_receipt = self.evidence / "operator-receipt.json"
        generic_receipt.write_text('{"observation":"redacted fixture"}')
        for other in manifest["checks"]:
            if other["name"] in {"native_cold_boot", "tasks_scale_from_zero"}:
                continue
            recorded = subprocess.run(
                [sys.executable, str(RUNNER), "record", "--dir", str(self.evidence),
                 "--name", other["name"], "--target", "operator fixture",
                 "--artifact", generic_receipt.name, "--status", "passed"],
                check=False, capture_output=True, text=True,
            )
            self.assertEqual(recorded.returncode, 0, recorded.stdout + recorded.stderr)
        scale_receipt = scale_zero_receipt("operator fixture")
        (self.evidence / "tasks-scale-zero.json").write_text(json.dumps(scale_receipt))
        recorded = subprocess.run(
            [sys.executable, str(RUNNER), "record", "--dir", str(self.evidence),
             "--name", "tasks_scale_from_zero", "--target", scale_receipt["target"],
             "--artifact", "tasks-scale-zero.json", "--status", "passed"],
            check=False, capture_output=True, text=True,
        )
        self.assertEqual(recorded.returncode, 0, recorded.stdout + recorded.stderr)
        self.assertTrue(self._checker().check(self.evidence / "manifest.json", self.commit)["ok"])

    def test_wrong_wake_tier_is_failed_and_cannot_be_recorded_as_passed(self):
        result = self.probe(extra_env={"FAKE_WAKE_TIER": "restored"})
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        manifest = json.loads((self.evidence / "manifest.json").read_text())
        row = next(row for row in manifest["checks"] if row["name"] == "native_cold_boot")
        self.assertEqual(row["status"], "failed")

        record = subprocess.run(
            [sys.executable, str(RUNNER), "record", "--dir", str(self.evidence),
             "--name", "native_cold_boot", "--target", row["target"],
             "--artifact", row["artifact"], "--status", "passed"],
            check=False, capture_output=True, text=True,
        )
        self.assertEqual(record.returncode, 1)
        self.assertIn("cannot be changed", record.stdout)

        row["status"] = "passed"
        (self.evidence / "manifest.json").write_text(json.dumps(manifest))
        with self.assertRaisesRegex(ValueError, "does not prove a successful cold wake"):
            checker = self._checker()
            checker.check(self.evidence / "manifest.json", self.commit)

    def test_scale_zero_rejects_generic_operator_receipt(self):
        artifact = self.evidence / "operator-scale-zero.json"
        artifact.write_text('{"observation":"worker pool returned to zero"}')
        result = subprocess.run(
            [sys.executable, str(RUNNER), "record", "--dir", str(self.evidence),
             "--name", "tasks_scale_from_zero", "--target", "operator fixture",
             "--artifact", artifact.name, "--status", "passed"],
            check=False, capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 1)
        self.assertIn("validated staging-runner receipt", result.stdout)

    def test_park_restore_requires_restored_wake_tier(self):
        result = self.probe(name="native_park_restore", extra_env={"FAKE_WAKE_TIER": "restored"})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_probe_fails_when_final_park_leaves_an_active_instance(self):
        result = self.probe(extra_env={"FAKE_ACTIVE_INSTANCE": "true"})
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        manifest = json.loads((self.evidence / "manifest.json").read_text())
        row = next(row for row in manifest["checks"] if row["name"] == "native_cold_boot")
        self.assertEqual(row["status"], "failed")

    def test_confirmation_must_match_staging_app_before_any_cli_call(self):
        log = self.root / "called"
        self.fake_gregale.write_text(
            "#!/usr/bin/env python3\n"
            "from pathlib import Path\n"
            f"Path({str(log)!r}).write_text('called')\n"
        )
        result = self.probe(confirmation="production-app")
        self.assertEqual(result.returncode, 1)
        self.assertIn("must exactly match", result.stdout)
        self.assertFalse(log.exists())

    @staticmethod
    def _checker():
        module_spec = importlib.util.spec_from_file_location(
            "mcp_qualification_checker", REPO / "scripts" / "ci" / "mcp-qualification-check.py"
        )
        checker = importlib.util.module_from_spec(module_spec)
        module_spec.loader.exec_module(checker)
        return checker


class TaskRestartQualificationTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.evidence = self.root / "evidence"
        self.commit = "b" * 40
        initialized = subprocess.run(
            [sys.executable, str(RUNNER), "init", "--dir", str(self.evidence), "--commit", self.commit],
            check=False, capture_output=True, text=True,
        )
        if initialized.returncode != 0:
            self.fail(initialized.stdout + initialized.stderr)
        self.starter = self.root / "mcp-node"
        (self.starter / "test").mkdir(parents=True)
        (self.starter / "node_modules" / "pg").mkdir(parents=True)
        (self.starter / "test" / "tasks-restart-qualification.mjs").write_text("// fixture runner\n")
        (self.starter / "node_modules" / "pg" / "package.json").write_text('{"name":"pg"}')

    def test_tasks_restart_records_valid_redacted_runner_receipt(self):
        receipt = {
            "version": 1,
            "runner": "gregale-mcp-tasks-restart-v1",
            "check": "tasks_restart_replicas",
            "status": "passed",
            "target": "Node starter and disposable PostgreSQL fixture",
            "observed_at": "2026-10-10T12:00:00+00:00",
            "checks": {
                "initial_worker_crash": "passed",
                "two_replica_recovery": "passed",
                "stale_lease_fenced": "passed",
                "result_owner_isolation": "passed",
                "final_attempt_input_resume": "passed",
            },
            "observations": {
                "replacement_replicas": 2,
                "recovery_attempt": 2,
                "recovered_handler_executions": 1,
                "final_attempt": 3,
                "resumed_attempt": 3,
            },
            "failures": [],
        }
        fake_node = self.root / "node-fixture"
        fake_node.write_text(
            "#!/usr/bin/env python3\n"
            "import json, os, sys\n"
            "assert os.environ['MCP_TASKS_TEST_DATABASE_URL'] == 'postgres://user:secret@db.invalid/mcp'\n"
            "assert not any('secret' in value for value in sys.argv)\n"
            f"print({json.dumps(receipt)!r})\n"
        )
        fake_node.chmod(0o700)
        env = os.environ.copy()
        env["MCP_TASKS_TEST_DATABASE_URL"] = "postgres://user:secret@db.invalid/mcp"
        result = subprocess.run(
            [sys.executable, str(RUNNER), "tasks-restart", "--dir", str(self.evidence),
             "--target", receipt["target"], "--starter-dir", str(self.starter),
             "--confirm-disposable-database", "--node", str(fake_node)],
            check=False, capture_output=True, text=True, env=env,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        manifest = json.loads((self.evidence / "manifest.json").read_text())
        row = next(row for row in manifest["checks"] if row["name"] == "tasks_restart_replicas")
        self.assertEqual(row["evidence_type"], "gregale-mcp-tasks-restart-v1")
        receipt_path = self.evidence / row["artifact"]
        self.assertNotIn("postgres://", receipt_path.read_text())
        self.assertNotIn("secret", receipt_path.read_text())
        self.assertTrue(self._checker().validate_tasks_restart_receipt(
            json.loads(receipt_path.read_text()), row["name"], row["target"], require_passed=False
        ) is None)

    @staticmethod
    def _checker():
        module_spec = importlib.util.spec_from_file_location(
            "mcp_qualification_checker", REPO / "scripts" / "ci" / "mcp-qualification-check.py"
        )
        checker = importlib.util.module_from_spec(module_spec)
        module_spec.loader.exec_module(checker)
        return checker


if __name__ == "__main__":
    unittest.main()
