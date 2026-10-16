import importlib.util
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("qualification", Path(__file__).with_name("mcp-qualification-check.py"))
qualification = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qualification)


def scale_zero_receipt(target="fixture"):
    return {
        "version": 2,
        "runner": qualification.TASKS_SCALE_ZERO_RUNNER,
        "check": "tasks_scale_from_zero",
        "status": "passed",
        "target": target,
        "app": "test-staging",
        "observer_app": "test-observer",
        "observed_at": "2026-10-10T12:00:00+00:00",
        "checks": {name: "passed" for name in qualification.TASKS_SCALE_ZERO_CHECKS},
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


class QualificationGateTest(unittest.TestCase):
    def test_sdk_interop_receipt_requires_prompt_and_resource_completions(self):
        runtimes = []
        for runtime in ("node", "go", "python"):
            protocols = []
            for mode, version, era in (
                ("modern", "2026-07-28", "modern"),
                ("legacy", "2025-11-25", "legacy"),
            ):
                protocols.append({
                    "mode": mode,
                    "protocol_era": era,
                    "protocol_version": version,
                    "checks": {
                        "discovery": "passed" if mode == "modern" else "not_applicable",
                        "initialize": "passed",
                        "tools_list": "passed",
                        "tool_calls": "passed",
                        "resources": "passed",
                        "prompts": "passed",
                        "completions": "passed",
                        "progress": "passed",
                        "progress_events": 3,
                    },
                    "tool_count": 3,
                    "resource_count": 1,
                    "resource_template_count": 1,
                    "prompt_count": 1,
                    "completion_count": 2,
                })
            runtimes.append({
                "runtime": runtime,
                "capabilities": ["completions", "prompts", "resources", "tools"],
                "protocols": protocols,
            })
        receipt = {
            "version": 1,
            "runner": qualification.SDK_INTEROP_RUNNER,
            "check": "official_sdk_interop",
            "status": "passed",
            "target": "starter fixture",
            "observed_at": "2026-10-10T12:00:00+00:00",
            "sdk": {
                "package": "@modelcontextprotocol/client",
                "version": qualification.SDK_INTEROP_VERSION,
            },
            "runtimes": runtimes,
            "failures": [],
        }
        record = {"evidence_type": qualification.SDK_INTEROP_RUNNER, "target": receipt["target"]}
        with tempfile.TemporaryDirectory() as directory:
            artifact = Path(directory) / "interop.json"
            artifact.write_text(json.dumps(receipt))
            qualification.check_sdk_interop(artifact, "official_sdk_interop", record)

            receipt["runtimes"][1]["protocols"][0]["checks"].pop("completions")
            artifact.write_text(json.dumps(receipt))
            with self.assertRaisesRegex(ValueError, "protocol result is invalid"):
                qualification.check_sdk_interop(artifact, "official_sdk_interop", record)

    def test_incomplete_and_changed_evidence_cannot_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifact = root / "receipt.json"
            artifact.write_text('{"observed":"fixture"}')
            scale_artifact = root / "tasks-scale-zero.json"
            scale_artifact.write_text(json.dumps(scale_zero_receipt()))
            manifest = root / "manifest.json"
            commit = "a" * 40
            records = []
            for name in sorted(qualification.REQUIRED):
                selected_artifact = scale_artifact if name == "tasks_scale_from_zero" else artifact
                records.append({
                    "name": name,
                    "status": "passed",
                    "target": "fixture",
                    "artifact": selected_artifact.name,
                    "sha256": hashlib.sha256(selected_artifact.read_bytes()).hexdigest(),
                    **({"evidence_type": qualification.TASKS_SCALE_ZERO_RUNNER}
                       if name == "tasks_scale_from_zero" else {}),
                })
            evidence = {"version": 1, "commit": commit, "checks": records}
            manifest.write_text(json.dumps(evidence))
            self.assertTrue(qualification.check(manifest, commit)["ok"])
            for mutation in ("missing", "pending", "source", "digest", "escape"):
                changed = json.loads(json.dumps(evidence))
                if mutation == "missing": changed["checks"].pop()
                if mutation == "pending": changed["checks"][0]["status"] = "pending"
                if mutation == "source": changed["commit"] = "b" * 40
                if mutation == "digest": changed["checks"][0]["sha256"] = "0" * 64
                if mutation == "escape": changed["checks"][0]["artifact"] = "../receipt.json"
                manifest.write_text(json.dumps(changed))
                with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                    qualification.check(manifest, commit)

    def test_task_restart_runner_receipt_proves_two_replica_recovery(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            receipt = {
                "version": 1,
                "runner": qualification.TASKS_RESTART_RUNNER,
                "check": "tasks_restart_replicas",
                "status": "passed",
                "target": "starter and disposable database fixture",
                "observed_at": "2026-10-10T12:00:00+00:00",
                "checks": {name: "passed" for name in qualification.TASKS_RESTART_CHECKS},
                "observations": {
                    "replacement_replicas": 2,
                    "recovery_attempt": 2,
                    "recovered_handler_executions": 1,
                    "final_attempt": 3,
                    "resumed_attempt": 3,
                },
                "failures": [],
            }
            artifact = root / "tasks-restart.json"
            artifact.write_text(json.dumps(receipt))
            scale_artifact = root / "tasks-scale-zero.json"
            scale_artifact.write_text(json.dumps(scale_zero_receipt()))
            manifest = root / "manifest.json"
            commit = "c" * 40
            records = []
            for name in sorted(qualification.REQUIRED):
                selected_artifact = scale_artifact if name == "tasks_scale_from_zero" else artifact
                records.append({
                    "name": name,
                    "status": "passed",
                    "target": receipt["target"] if name == "tasks_restart_replicas" else "fixture",
                    "artifact": selected_artifact.name,
                    "sha256": hashlib.sha256(selected_artifact.read_bytes()).hexdigest(),
                    **({"evidence_type": qualification.TASKS_RESTART_RUNNER}
                       if name == "tasks_restart_replicas" else {}),
                    **({"evidence_type": qualification.TASKS_SCALE_ZERO_RUNNER}
                       if name == "tasks_scale_from_zero" else {}),
                })
            manifest.write_text(json.dumps({"version": 1, "commit": commit, "checks": records}))
            self.assertTrue(qualification.check(manifest, commit)["ok"])

            for mutation in ("one_replica", "stale_lease", "retry_budget", "failure"):
                changed = json.loads(json.dumps(receipt))
                if mutation == "one_replica":
                    changed["observations"]["replacement_replicas"] = 1
                elif mutation == "stale_lease":
                    changed["checks"]["stale_lease_fenced"] = "failed"
                elif mutation == "retry_budget":
                    changed["observations"]["resumed_attempt"] = 4
                else:
                    changed["failures"] = [{"stage": "runtime", "reason": "unredacted error"}]
                artifact.write_text(json.dumps(changed))
                record = next(row for row in records if row["name"] == "tasks_restart_replicas")
                record["sha256"] = hashlib.sha256(artifact.read_bytes()).hexdigest()
                manifest.write_text(json.dumps({"version": 1, "commit": commit, "checks": records}))
                with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                    qualification.check(manifest, commit)

    def test_task_scale_zero_receipt_requires_every_transition(self):
        receipt = scale_zero_receipt("reviewed staging target")
        self.assertIsNone(qualification.validate_tasks_scale_zero_receipt(
            receipt, "tasks_scale_from_zero", receipt["target"]
        ))

        for mutation in (
            "no_backlog", "no_worker", "no_return_to_zero",
            "observer_missing_before", "observer_stale_throughout", "observer_not_ready",
        ):
            changed = json.loads(json.dumps(receipt))
            if mutation == "no_backlog":
                changed["observations"]["peak_outstanding"] = 0
            elif mutation == "no_worker":
                changed["observations"]["peak_replicas"] = 0
            elif mutation == "no_return_to_zero":
                changed["observations"]["final_replicas"] = 1
            elif mutation == "observer_missing_before":
                changed["checks"]["observer_heartbeat_fresh_before"] = "failed"
            elif mutation == "observer_not_ready":
                changed["observations"]["observer_initial_replicas"] = 0
            else:
                changed["checks"]["observer_heartbeat_fresh_throughout"] = "failed"
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                qualification.validate_tasks_scale_zero_receipt(
                    changed, "tasks_scale_from_zero", receipt["target"]
                )

    def test_generic_operator_receipt_cannot_satisfy_scale_zero_gate(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifact = root / "operator.json"
            artifact.write_text('{"observation":"worker pool appeared healthy"}')
            records = [{
                "name": name,
                "status": "passed",
                "target": "fixture",
                "artifact": artifact.name,
                "sha256": hashlib.sha256(artifact.read_bytes()).hexdigest(),
            } for name in sorted(qualification.REQUIRED)]
            manifest = root / "manifest.json"
            commit = "d" * 40
            manifest.write_text(json.dumps({"version": 1, "commit": commit, "checks": records}))
            with self.assertRaisesRegex(ValueError, "validated staging-runner evidence"):
                qualification.check(manifest, commit)


if __name__ == "__main__":
    unittest.main()
