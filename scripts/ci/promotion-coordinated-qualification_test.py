#!/usr/bin/env python3
"""Synthetic parser fixtures only; these never qualify native hardware."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest
import uuid

spec = importlib.util.spec_from_file_location("coordinated_qualification", Path(__file__).with_name("promotion-coordinated-qualification.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
SHA = "1" * 40


def uid(index):
    return str(uuid.UUID(int=index))


def finished(n, rollback=False):
    item = dict(preflight_rounds=2, candidate_jobs=10, cutover_graph=uid(n+10), selected_graph=uid(n+11) if rollback else uid(n+10),
                phase="failed" if rollback else "succeeded", graph_config_restored=rollback,
                worker_deployment=uid(n+12), worker_instance=uid(n+13), worker_regression=rollback, regressed_rounds=2 if rollback else 0,
                healthy_windows=["2026-10-16T10:00:00Z", "2026-10-16T10:01:00Z"], health_started_at="2026-10-16T10:00:00Z")
    item.update(destination_config_hash="a"*64, baseline_config_hash="b"*64, selected_config_hash=("b" if rollback else "a")*64)
    item.update(dict.fromkeys(("restart_preflight", "restart_verifying", "candidate_recovered", "destination_secrets", "artifact_reused"), True))
    item["private_report"] = dict(deployment_id=uid(n+14), backend_deployment_id=uid(n+15), instance_id=uid(n+16), backend_instance_id=uid(n+17),
                                  generation="tested", backend_generation="tested", mode="tested-config", backend_mode="tested-config",
                                  release_id=uid(n), backend_release_id=uid(n), binding_transport="https",
                                  destination_secret_matches=True, backend_destination_secret_matches=True)
    return item


def observations():
    records = {}
    for index, (name, scenario) in enumerate(module.SCENARIOS.items()):
        n = 100 + index * 100
        item = dict(schema_version=1, scenario=scenario, promotion_id=uid(n), task_id=uid(n+1), run_id=uid(n+2), baseline_graph=uid(n+3),
                    migration_owner=uid(n+4), worker_app=uid(n+5), executions=1, task_count=1, attempts=1, exit_code=0)
        item.update(dict.fromkeys(("cleanup", "worker_dormant", "migration_restart", "destination_only", "schema_preserved", "terminal_restart"), True))
        if scenario != "coordinated_resolve":
            item.update(finished(n, scenario == "coordinated_rollback"))
            item.update(retry_attempts=2, retry_release=uid(n+3), retry_deployment=uid(n+18))
        else:
            repair = finished(n+30)
            sample = dict(attempts=5, failures=0, retries=0, incomplete=0, duration_ms=5, queue_delay_ms=0)
            item["exit_code"] = None
            item["recovery"] = dict(repair_promotion=uid(n+30), repair=repair, task_id=uid(n+1), runtime_id=uid(n+50), status="resolved",
                                    requested_at="2026-10-16T10:02:00Z", destroyed_at="2026-10-16T10:02:01Z", resolved_at="2026-10-16T10:03:00Z",
                                    session_gone=True, receipt_preserved=True, admission_disabled=True,
                                    worker_proof=dict(window_start="2026-10-16T10:02:00Z", checked_at="2026-10-16T10:02:59Z",
                                                      worker=dict(app_id=uid(n+5), deployment_id=repair["worker_deployment"], previous_deployment_id=repair["worker_deployment"], instance_id=repair["worker_instance"]),
                                                      health=dict(status="healthy", baseline=sample.copy(), candidate=sample.copy())))
        records[name] = item
    return records


def log(records=None):
    if records is None:
        records = observations()
    lines = [f"=== RUN   {module.META}", f"--- PASS: {module.META} (0.01s)"]
    for name, record in records.items():
        lines += [f"=== RUN   {name}", f"    fixture.go:1: environment promotion native: {json.dumps(record)}", f"--- PASS: {name} (300.00s)"]
    lines += ["PASS", f"native promotion qualification: final_leakcheck_rc=0 group={module.GROUP}",
              f"native promotion qualification: sha={SHA} final_rc=0 group={module.GROUP}"]
    return "\n".join(lines) + "\n"


class QualificationTests(unittest.TestCase):
    def reject(self, records):
        report = module.qualify(log(records), SHA)
        self.assertFalse(report["qualified"], report)

    def test_complete_exact_supplement(self):
        report = module.qualify(log(), SHA)
        self.assertTrue(report["qualified"], report["errors"])
        self.assertFalse(report["admission_enabled"])
        self.assertEqual(report["scope"], "project-environment-promotion-only/promotion-coordinated")
        self.assertEqual(set(report["tests"]), module.TESTS)

    def test_source_cleanup_and_inventory(self):
        original = log()
        runtime = next(iter(module.SCENARIOS))
        values = [original.replace(SHA, "2"*40), original.replace("final_leakcheck_rc=0", "final_leakcheck_rc=1"),
                  original.replace("final_rc=0", "final_rc=1"), original.replace(f"--- PASS: {runtime}", f"--- SKIP: {runtime}"),
                  original.replace(f"=== RUN   {runtime}\n", ""), original+f"--- PASS: {runtime} (1s)\n",
                  original+"=== RUN   Unexpected\n--- PASS: Unexpected (1s)\n"]
        for value in values:
            self.assertFalse(module.qualify(value, SHA)["qualified"])
        for name in module.TESTS:
            self.assertFalse(module.qualify("\n".join(line for line in original.splitlines() if name not in line), SHA)["qualified"])

    def test_missing_execution_or_proof(self):
        for name in module.SCENARIOS:
            for field in ("executions", "task_count", "attempts"):
                for value in (0, 2, True, "1"):
                    records = observations(); records[name][field] = value; self.reject(records)
            for field in ("worker_dormant", "migration_restart", "destination_only", "schema_preserved", "terminal_restart", "cleanup"):
                records = observations(); records[name][field] = False; self.reject(records)

    def test_mixed_graph_and_wrong_job_pin(self):
        name = module.PREFIX+"SuccessMetal"
        for field, value in (("backend_deployment_id", ""), ("backend_release_id", uid(999)), ("backend_mode", "staging-config"), ("backend_destination_secret_matches", False), ("binding_transport", "http")):
            records = observations(); records[name]["private_report"][field] = value; self.reject(records)
        for field, value in (("worker_deployment", ""), ("retry_attempts", 1), ("retry_release", uid(999)), ("preflight_rounds", 1), ("selected_config_hash", "b"*64)):
            records = observations(); records[name][field] = value; self.reject(records)
        records = observations(); records[name]["healthy_windows"] *= 2; self.reject(records)

    def test_worker_rollback_needs_worker_regression(self):
        name = module.PREFIX+"RollbackMetal"
        for field, value in (("regressed_rounds", 1), ("worker_regression", False), ("graph_config_restored", False), ("selected_graph", observations()[name]["baseline_graph"])):
            records = observations(); records[name][field] = value; self.reject(records)

    def test_resolution_needs_exact_runtime_and_fresh_owned_samples(self):
        name = module.PREFIX+"ResolveMetal"
        for field, value in (("destroyed_at", "2026-10-16T10:01:00Z"), ("status", "resumed"), ("session_gone", False), ("receipt_preserved", False), ("admission_disabled", False)):
            records = observations(); records[name]["recovery"][field] = value; self.reject(records)
        for field, value in (("window_start", "2026-10-16T10:01:00Z"), ("checked_at", "2026-10-16T10:04:00Z")):
            records = observations(); records[name]["recovery"]["worker_proof"][field] = value; self.reject(records)
        for side in ("baseline", "candidate"):
            for field, value in (("attempts", 4), ("failures", 1), ("incomplete", 1), ("retries", 1), ("duration_ms", -1)):
                records = observations(); records[name]["recovery"]["worker_proof"]["health"][side][field] = value; self.reject(records)
        for field in ("deployment_id", "previous_deployment_id", "app_id", "instance_id"):
            records = observations(); records[name]["recovery"]["worker_proof"]["worker"][field] = ""; self.reject(records)
        records = observations(); records[name]["exit_code"] = 0; self.reject(records)

    def test_reused_and_malformed_evidence(self):
        for value in (None, [], True, {"schema_version": 1}):
            records = observations(); records[next(iter(records))] = value; self.reject(records)
        records = observations(); names = list(records)
        for field in ("promotion_id", "task_id", "run_id"):
            altered = copy.deepcopy(records); altered[names[1]][field] = altered[names[0]][field]; self.reject(altered)


if __name__ == "__main__":
    unittest.main()
