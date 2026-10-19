import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import uuid

import provider_qualification as q
import provider_fault_adapter as adapter
from staging_canary import CanaryFailure


def arguments(**overrides):
    values = dict(timeout=240, hold_seconds=5, adapter="/private/adapter", pipeline=str(uuid.uuid4()),
                  connection=str(uuid.uuid4()), database=str(uuid.uuid4()), admission_database=str(uuid.uuid4()),
                  backend_fingerprint="a" * 64, source_url_file="source", rotated_source_url_file="rotated",
                  reader_token_file="reader", other_token_file="other", control_token_file="control",
                  data_api_url="https://data.example", name="pipeline-canary-run")
    values.update(overrides)
    return argparse.Namespace(**values)


class QualificationTests(unittest.TestCase):
    def test_prerequisites_fail_closed_without_remote_calls(self):
        with patch.dict(os.environ, {}, clear=True), patch("subprocess.run") as remote:
            missing = q.prerequisites(arguments())
        self.assertIn("staging_environment_required", missing)
        self.assertIn("source_url_file_unavailable_or_invalid", missing)
        self.assertIn("reviewed_private_adapter_required", missing)
        remote.assert_not_called()

    def test_adapter_must_be_owner_private_executable_and_not_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "adapter"
            path.write_text("#!/bin/sh\nexit 0\n")
            path.chmod(0o700)
            self.assertEqual(q.executable_digest(str(path)), hashlib.sha256(path.read_bytes()).hexdigest())
            path.chmod(0o755)
            with self.assertRaises(CanaryFailure):
                q.executable_digest(str(path))
            link = path.with_name("link")
            link.symlink_to(path)
            with self.assertRaises(OSError):
                q.executable_digest(str(link))

    def test_partial_fault_application_still_restores(self):
        runner = q.Qualification(arguments())
        calls = []
        def execute(action, nonce):
            calls.append((action, nonce))
            if action == "source_disconnect":
                raise subprocess.TimeoutExpired("adapter", 1)
            return {}
        with patch.object(runner, "adapter", side_effect=execute):
            with self.assertRaises(subprocess.TimeoutExpired), runner.fault("source_disconnect"):
                self.fail("fault entry should fail")
        self.assertEqual([c[0] for c in calls], ["source_disconnect", "source_reconnect"])
        self.assertEqual(calls[0][1], calls[1][1])
        self.assertEqual(runner.active_faults, {})

    def test_restore_failure_retains_explicit_manual_recovery_evidence(self):
        runner = q.Qualification(arguments())
        with patch.object(runner, "adapter", side_effect=[{}, CanaryFailure("failed")]):
            with self.assertRaisesRegex(CanaryFailure, "fault_restore_failed"), runner.fault("worker_stop"):
                pass
        self.assertIn("worker_stop", runner.active_faults)
        self.assertEqual(runner.restore_failures, ["worker_start"])

    def test_fault_is_restored_on_interruption(self):
        runner = q.Qualification(arguments())
        with patch.object(runner, "adapter", return_value={}) as execute:
            with self.assertRaises(KeyboardInterrupt), runner.fault("destination_disconnect"):
                raise KeyboardInterrupt
        self.assertEqual(execute.call_args_list[-1].args[0], "destination_reconnect")

    def test_adapter_receipt_is_bound_to_this_run_and_nonce(self):
        runner = q.Qualification(arguments())
        runner.adapter_sha256 = "digest"
        receipt = dict(version=1, run_id=runner.run_id, nonce="nonce", action="preflight",
                       backend_fingerprint=runner.args.backend_fingerprint, applied=True)
        with patch.object(q, "executable_digest", return_value="digest"), patch("subprocess.run") as command:
            command.return_value = subprocess.CompletedProcess([], 0, json.dumps(receipt), "private-sentinel")
            self.assertTrue(runner.adapter("preflight", "nonce")["applied"])
            receipt["run_id"] = str(uuid.uuid4())
            command.return_value.stdout = json.dumps(receipt)
            with self.assertRaisesRegex(CanaryFailure, "receipt_mismatch"):
                runner.adapter("preflight", "nonce")
            self.assertNotIn("private-sentinel", str(command.call_args.args))
            self.assertNotIn("shell", command.call_args.kwargs)

    def test_changed_adapter_cannot_run(self):
        runner = q.Qualification(arguments())
        runner.adapter_sha256 = "old"
        with patch.object(q, "executable_digest", return_value="changed"), patch("subprocess.run") as command:
            with self.assertRaisesRegex(CanaryFailure, "adapter_changed"):
                runner.adapter("worker_stop", "nonce")
            command.assert_not_called()

    def test_unobserved_outage_is_a_failure_and_restores(self):
        runner = q.Qualification(arguments())
        with patch.object(runner, "adapter", return_value={}) as actions, \
             patch.object(runner, "write_marker", side_effect=["before", "during"]), \
             patch.object(runner, "await_value"), \
             patch.object(runner, "await_status", side_effect=CanaryFailure("outage_not_observed")):
            with self.assertRaisesRegex(CanaryFailure, "outage_not_observed"):
                runner.outage(False)
        self.assertEqual(actions.call_args_list[-1].args[0], "source_reconnect")

    def test_destination_outage_requires_service_failure_not_unrelated_auth_failure(self):
        runner = q.Qualification(arguments())
        runner.reader = "token"
        with patch.object(runner, "http", return_value=(401, {})):
            with self.assertRaisesRegex(CanaryFailure, "destination_outage_not_observed"):
                runner.unchanged_during_fault("value", True)

    def test_capacity_requires_real_atomic_denial_not_a_compatibility_check(self):
        runner = q.Qualification(arguments())
        runner.reader = "reader"
        with patch.object(runner, "cli", return_value={}), patch.object(runner, "adapter", return_value={}) as actions, \
             patch.object(q, "jwt_claims", return_value={"sub": "reader"}), \
             patch.object(runner, "control", return_value=(409, {"code": "data_pipeline_capacity_needs_qualification"})):
            with self.assertRaisesRegex(CanaryFailure, "atomic_capacity_denial_not_observed"):
                runner.capacity_exhaustion()
        self.assertEqual(actions.call_args_list[-1].args[0], "capacity_release")

    def test_retention_loss_never_automatically_resyncs(self):
        runner = q.Qualification(arguments())
        with patch.object(runner, "status", return_value={"state": "resync_required"}), patch.object(runner, "cli") as cli:
            with self.assertRaisesRegex(CanaryFailure, "unexpected_resync_required"):
                runner.await_status(lambda p: False, "timeout")
            cli.assert_not_called()

    def test_cleanup_checks_all_generations_then_private_target_audit(self):
        runner = q.Qualification(arguments())
        receipt = {"target_subscription_count": 0, "pipeline_reservation_count": 0, "fault_resource_count": 0}
        with patch.object(runner, "sql", return_value="t") as sql, patch.object(runner, "adapter", return_value=receipt):
            runner.confirm_cleanup({"id": runner.args.pipeline})
            self.assertIn("database=current_database()", sql.call_args.args[0])
            self.assertNotIn("slot_name=", sql.call_args.args[0])
            receipt["target_subscription_count"] = 1
            with self.assertRaisesRegex(CanaryFailure, "provider_cleanup_incomplete"):
                runner.confirm_cleanup({"id": runner.args.pipeline})

    def test_report_preflight_never_claims_live_evidence_or_overwrites(self):
        with tempfile.TemporaryDirectory() as directory:
            path = str(Path(directory) / "report.json")
            with patch("sys.argv", ["qualify", "--check-config", "--report", path]), \
                 patch.object(q, "prerequisites", return_value=["staging_credentials_missing"]), \
                 patch.object(q.Qualification, "run") as run, patch("sys.stdout", new_callable=io.StringIO):
                self.assertEqual(q.main(), 1)
                run.assert_not_called()
            report = json.loads(Path(path).read_text())
            self.assertEqual(report["state"], "blocked")
            self.assertFalse(report["production_ready"])
            self.assertFalse(report["pilot_ready"])
            self.assertTrue(all(phase["state"] == "not_run" for phase in report["phases"]))
            self.assertEqual(os.stat(path).st_mode & 0o777, 0o600)
            with patch("sys.argv", ["qualify", "--check-config", "--report", path]), patch.object(q, "prerequisites") as check:
                with self.assertRaises(FileExistsError):
                    q.main()
                check.assert_not_called()

    def test_passing_harness_keeps_operator_review_and_records_skipped_scenarios(self):
        def partial(runner):
            runner.passed("initial_copy")
        with tempfile.TemporaryDirectory() as directory:
            path = str(Path(directory) / "report.json")
            with patch("sys.argv", ["qualify", "--report", path]), patch.object(q, "prerequisites", return_value=[]), \
                 patch.object(q.Qualification, "run", partial), patch("sys.stdout", new_callable=io.StringIO):
                q.main()
            report = json.loads(Path(path).read_text())
            self.assertEqual(report["state"], "failed")
            self.assertIn("incomplete_qualification_evidence", report["blockers"])
            self.assertFalse(report["pilot_ready"])
            self.assertEqual(report["operator_review"], "required")
            self.assertTrue(any(p["state"] == "not_run" for p in report["phases"]))


class FaultAdapterTests(unittest.TestCase):
    def fixture(self):
        args = arguments()
        request = dict(version=1, run_id=str(uuid.uuid4()), nonce=str(uuid.uuid4()), action="worker_stop",
                       source_connection_id=args.connection, database_id=args.database,
                       admission_database_id=args.admission_database, backend_fingerprint=args.backend_fingerprint,
                       pipeline_id=args.pipeline, timeout_seconds=10, data_api_url=args.data_api_url)
        config = dict(request)
        config["commands"] = {"worker_stop": ["/usr/local/bin/staging-worker", "stop"]}
        return request, config

    def test_scope_mismatch_never_runs_a_command(self):
        request, config = self.fixture()
        request["database_id"] = str(uuid.uuid4())
        with patch.dict(os.environ, {"FAAS_ENVIRONMENT": "staging"}), patch("subprocess.run") as run:
            with self.assertRaisesRegex(CanaryFailure, "scope_mismatch"):
                adapter.execute(request, config)
            run.assert_not_called()

    def test_fixed_argv_and_receipts_filter_arbitrary_secret_output(self):
        request, config = self.fixture()
        with patch.dict(os.environ, {"FAAS_ENVIRONMENT": "staging"}), \
             patch("subprocess.run", return_value=subprocess.CompletedProcess([], 0, '{"credential":"secret-sentinel"}', "")) as run:
            receipt = adapter.execute(request, config)
        self.assertNotIn("secret-sentinel", json.dumps(receipt))
        self.assertEqual(run.call_args.args[0], config["commands"]["worker_stop"])
        self.assertNotIn("shell", run.call_args.kwargs)

    def test_preflight_requires_complete_restore_and_audit_commands(self):
        request, config = self.fixture()
        request["action"] = "preflight"
        with patch.dict(os.environ, {"FAAS_ENVIRONMENT": "staging"}), patch("subprocess.run") as run:
            with self.assertRaisesRegex(CanaryFailure, "complete_adapter_commands_required"):
                adapter.execute(request, config)
            run.assert_not_called()



class QualificationEvidenceTests(unittest.TestCase):
    def test_expiry_and_wal_threshold_contract_matches_server(self):
        limits = (Path(__file__).resolve().parents[2] / "pkg/api/limits.go").read_text()
        self.assertRegex(limits, r"DataPipelineObservationMaxAge\s*=\s*" + str(q.OBSERVATION_MAX_AGE) + r" \* time.Second")
        self.assertRegex(limits, r"DataPipelineRetainedWALMaxBytes\s*=\s*int64\(1 << 30\)")
        self.assertRegex(limits, r"DataPipelineRetentionWarningBytes\s*=\s*DataPipelineRetainedWALMaxBytes \* 4 / 5")
        self.assertEqual(q.RETENTION_WARNING_BYTES, (1 << 30) * 4 // 5)

    def test_capacity_denial_with_leaked_hold_is_not_qualified(self):
        runner = q.Qualification(arguments())
        runner.reader = "token"
        receipts = [{}, {"reservation_count": 3}, {"reservation_count": 4}, {}]
        with patch.object(runner, "cli", return_value={}), patch.object(runner, "adapter", side_effect=receipts) as actions, \
             patch.object(q, "jwt_claims", return_value={"sub": "reader"}), \
             patch.object(runner, "control", side_effect=[(409, {"code": "data_pipeline_insufficient_capacity"}), (200, {"items": []})]):
            with self.assertRaisesRegex(CanaryFailure, "leaked_reservations"):
                runner.capacity_exhaustion()
        self.assertEqual(actions.call_args_list[-1].args[0], "capacity_release")

    def test_worker_does_not_qualify_artificially_early_staleness(self):
        runner = q.Qualification(arguments())
        runner.reader = "token"
        before = {"generation": 1}
        with patch.object(runner, "status", return_value=before), patch.object(runner, "require_capacity"), \
             patch.object(runner, "adapter", return_value={}) as actions, patch.object(runner, "write_marker", return_value="marker"), \
             patch.object(runner, "await_value"), \
             patch.object(runner, "await_status", return_value={"observed_at": datetime.now(timezone.utc).isoformat()}):
            with self.assertRaisesRegex(CanaryFailure, "expiry_not_observed"):
                runner.worker_restart()
        self.assertEqual(actions.call_args_list[-1].args[0], "worker_start")

    def test_all_scenarios_required_even_for_a_passing_report(self):
        def simulated(runner):
            for name in q.SCENARIOS:
                runner.passed(name)
        with tempfile.TemporaryDirectory() as directory:
            path = str(Path(directory) / "report.json")
            with patch("sys.argv", ["qualify", "--report", path]), patch.object(q, "prerequisites", return_value=[]), \
                 patch.object(q.Qualification, "run", simulated), patch("sys.stdout", new_callable=io.StringIO):
                self.assertEqual(q.main(), 0)
            report = json.loads(Path(path).read_text())
            self.assertEqual(report["state"], "passed")
            self.assertFalse(report["pilot_ready"])
            self.assertFalse(report["production_ready"])
            required = {"provider_qualification.py", "provider_fault_adapter.py", "staging_canary.py"}
            self.assertTrue(required.issubset(report["evidence_sha256"]))
            self.assertTrue(set(report["evidence_sha256"]).issubset(required | {"gregale_binary"}))
            for digest in report["evidence_sha256"].values():
                self.assertRegex(digest, r"^[0-9a-f]{64}$")

class CanaryExtensionTests(unittest.TestCase):
    def test_recovery_drills_run_before_cleanup_and_cleanup_is_audited_before_connection_delete(self):
        from staging_canary import Canary
        args = arguments()
        runner = Canary(args)
        events = []
        now = datetime.now(timezone.utc)
        endpoint = {"state": "ready", "required_connections": 0, "reserved_connections": 5,
                    "usable_connections": 100, "observed_at": now.isoformat(),
                    "expires_at": (now + timedelta(seconds=30)).isoformat()}
        pipeline = dict(id=args.pipeline, name="pipeline-canary-run", database_id=args.database,
                        source_connection_id=args.connection, tables=["pipeline_canary"], read_subjects=["reader"],
                        state="live", observation_fresh=True, revision=1, observed_revision=1,
                        capacity={"state": "ready", "source": endpoint, "destination": endpoint})
        def cli(*parts):
            events.append(parts[0])
            if parts[0] == "pause":
                return {**pipeline, "state": "paused"}
            if parts[0] == "delete":
                return {**pipeline, "state": "deleted"}
            if parts[0] == "verify":
                return {**pipeline, "verification": "passed"}
            return pipeline
        def files(path, limit):
            return "postgres://r:p@source.example/db?sslmode=verify-full" if path == args.source_url_file else path
        def claims(token):
            return {"sub": "reader" if token == args.reader_token_file else "other", "iss": "auth", "aud": "data"}
        def http(token, method="GET"):
            return (403, {"code": "read_only_identity"}) if method == "PATCH" else (200, [])
        with patch.dict(os.environ, {"FAAS_ENVIRONMENT": "staging", "FAAS_API": "https://control.example"}), \
             patch("staging_canary.private_file", side_effect=files), patch("staging_canary.jwt_claims", side_effect=claims), \
             patch.object(runner, "cli", side_effect=cli), patch.object(runner, "sql", return_value="t"), \
             patch.object(runner, "await_value"), patch.object(runner, "mirrored", return_value=True), \
             patch.object(runner, "http", side_effect=http), \
             patch.object(runner, "exercise_recovery", side_effect=lambda: events.append("recovery")), \
             patch.object(runner, "confirm_cleanup", side_effect=lambda p: events.append("audit")):
            runner.run()
        self.assertLess(events.index("recovery"), events.index("delete"))
        self.assertLess(events.index("delete"), events.index("audit"))
        self.assertLess(events.index("audit"), events.index("connection"))

class AdapterProcessTests(unittest.TestCase):
    def test_installed_private_adapter_round_trip_without_network_or_provider(self):
        import sys
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = Path(__file__).parent
            for filename in ("provider_qualification.py", "provider_fault_adapter.py", "staging_canary.py"):
                target = root / filename
                target.write_bytes((source / filename).read_bytes())
                target.chmod(0o700 if filename == "provider_fault_adapter.py" else 0o600)
            request, config = FaultAdapterTests().fixture()
            request["action"] = "preflight"
            # A harmless local command exercises the protocol, not live provider qualification.
            evidence = {"staging": True, "isolated_fixture": True, "source_retention_bytes": q.RETENTION_MAX_BYTES,
                        "shared_admission_scope": True, "provider_kind": "neon", "target_postgres_version": 160010,
                        "credential": "secret-sentinel"}
            command = [sys.executable, "-c", "import json;print(" + repr(json.dumps(evidence)) + ")"]
            config["commands"] = {action: command for action in adapter.ACTIONS}
            manifest = root / "manifest.json"
            manifest.write_text(json.dumps(config))
            manifest.chmod(0o600)
            env = {**os.environ, "FAAS_ENVIRONMENT": "staging", "PIPELINE_QUALIFICATION_ADAPTER_CONFIG": str(manifest)}
            result = subprocess.run([sys.executable, str(root / "provider_fault_adapter.py")], input=json.dumps(request),
                                    text=True, capture_output=True, timeout=10, env=env, check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            receipt = json.loads(result.stdout)
            self.assertEqual(receipt["nonce"], request["nonce"])
            self.assertEqual(len(receipt["control_manifest_sha256"]), 64)
            self.assertNotIn("secret-sentinel", result.stdout)

class InitialCopyTests(unittest.TestCase):
    def setup_runner(self):
        runner = q.Qualification(arguments())
        pipeline_id = str(uuid.uuid4())
        files = {
            "source": "postgres://r:old@source.example/db?sslmode=verify-full",
            "rotated": "postgres://r:new@source.example/db?sslmode=verify-full",
            "reader": "reader", "other": "other", "control": "control",
        }
        receipt = {"staging": True, "isolated_fixture": True, "shared_admission_scope": True,
                   "source_retention_bytes": q.RETENTION_MAX_BYTES, "provider_kind": "neon",
                   "target_postgres_version": 160010, "control_manifest_sha256": "b" * 64}
        return runner, pipeline_id, files, receipt

    def test_new_copy_and_schema_refresh_before_read_checks(self):
        runner, pipeline_id, files, receipt = self.setup_runner()
        events = []
        def execute(action, nonce):
            events.append(action)
            return receipt
        created = {"id": pipeline_id, "generation": 1, "database_id": runner.args.database,
                   "source_connection_id": runner.args.connection}
        claims = lambda token: {"sub": token, "iss": "auth", "aud": "data", "exp": q.time.time() + 7200}
        with patch.object(q, "private_file", side_effect=lambda path, limit: files[path]), \
             patch.object(q, "jwt_claims", side_effect=claims), patch.object(q, "executable_digest", return_value="digest"), \
             patch.object(runner, "sql", side_effect=["t", "t", "160010"]), patch.object(runner, "adapter", side_effect=execute), \
             patch.object(runner, "control", side_effect=[(200, {"items": []}), (200, {"id": pipeline_id})]), \
             patch.object(runner, "cli", side_effect=lambda *parts: (events.append("create") or created)), \
             patch.object(runner, "await_live", side_effect=lambda: events.append("live")):
            runner.prepare()
        self.assertEqual(events, ["preflight", "create", "live", "data_api_refresh"])
        self.assertEqual(runner.args.pipeline, pipeline_id)
        self.assertEqual(runner.phases[0], {"phase": "initial_copy", "state": "passed"})

    def test_existing_name_does_not_count_as_a_new_initial_copy(self):
        runner, pipeline_id, files, receipt = self.setup_runner()
        claims = lambda token: {"sub": token, "iss": "auth", "aud": "data", "exp": q.time.time() + 7200}
        with patch.object(q, "private_file", side_effect=lambda path, limit: files[path]), \
             patch.object(q, "jwt_claims", side_effect=claims), patch.object(q, "executable_digest", return_value="digest"), \
             patch.object(runner, "sql", side_effect=["t", "t", "160010"]), patch.object(runner, "adapter", return_value=receipt), \
             patch.object(runner, "control", return_value=(200, {"items": [{"name": runner.args.name}]})), \
             patch.object(runner, "cli") as cli:
            with self.assertRaisesRegex(CanaryFailure, "unused_pipeline_name"):
                runner.prepare()
            cli.assert_not_called()


class ManifestPinTests(unittest.TestCase):
    def test_changed_manifest_is_rejected_before_any_command(self):
        request, config = FaultAdapterTests().fixture()
        request["control_manifest_sha256"] = "f" * 64
        with patch.dict(os.environ, {"FAAS_ENVIRONMENT": "staging"}), patch("subprocess.run") as run:
            with self.assertRaisesRegex(CanaryFailure, "adapter_manifest_changed"):
                adapter.execute(request, config)
            run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
