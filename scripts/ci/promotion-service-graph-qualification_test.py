#!/usr/bin/env python3
"""Synthetic parser fixtures only; these do not qualify native hardware."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest
import uuid

spec = importlib.util.spec_from_file_location("graph_qualification", Path(__file__).with_name("promotion-service-graph-qualification.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
SHA = "1" * 40


def uid(index):
    return str(uuid.UUID(int=index))


def observation():
    result = dict(schema_version=1, scenario="service_graph", cleanup=True, cases={})
    for index, name in enumerate(("preflight_refusal", "rollback", "success")):
        n = 100 + index * 100
        item = dict(promotion_id=uid(n), private_graph=uid(n), baseline_graph=uid(n+1),
                    frontend_deployment=uid(n+2), backend_deployment=uid(n+3), initial_frontend_instance=uid(n+4), initial_backend_instance=uid(n+5),
                    source_config_hash="a"*64, destination_config_hash="b"*64, baseline_config_hash="c"*64,
                    phase="succeeded" if name=="success" else "failed", publications=index,
                    cutover_graph="" if index==0 else uid(n+6), selected_graph=uid(n+1) if index==0 else uid(n+7) if index==1 else uid(n+6),
                    preflight_rounds=2, regressed_rounds=2, restart_verifying=index!=0)
        item.update(preflight_generation=1, resumed_generation=1, preflight_deadline="2026-10-15T10:10:00Z", resumed_deadline="2026-10-15T10:10:00Z", health_started_at="2026-10-15T10:00:00Z")
        if name == "rollback": item["publications"] = 2
        if name == "success": item["publications"] = 1
        item.update(dict.fromkeys(("private_routing", "destination_secrets", "public_baseline", "checkpoint_preserved", "artifact_reused", "graph_config_consistent"), True))
        item["private_report"] = dict(deployment_id=uid(n+2), backend_deployment_id=uid(n+3), instance_id=uid(n+4), backend_instance_id=uid(n+5),
                                      release_id=uid(n), backend_release_id=uid(n), generation="tested", backend_generation="tested", mode="tested-config", backend_mode="tested-config",
                                      destination_secret_matches=True, backend_destination_secret_matches=True, binding_transport="https")
        if name == "success":
            item.update(cold_without_snapshot=True, cold_boot_event=True, park_wake=True, daemon_restarts=["apid", "vmmd", "gatewayd-internal", "schedd"],
                        healthy_windows=["2026-10-15T10:00:00Z", "2026-10-15T10:01:00Z"], parked_backend_instance=uid(n+8),
                        isolation=dict(owner_account=uid(n+20), foreign_account=uid(n+21), statuses=dict(same_tenant=409, foreign_tenant=403, candidate_wrong_hint=409),
                                       edge_statuses={"X-Gregale-Release":404,"Gregale-Target-Deployment":200,"X-Gregale-Revision":409}))
            item["recovered_report"] = copy.deepcopy(item["private_report"])
            item["recovered_report"]["backend_instance_id"] = uid(n+8)
            item["wake_report"] = copy.deepcopy(item["recovered_report"])
            item["wake_report"].update(backend_instance_id=uid(n+9), release_id=uid(n+6), backend_release_id=uid(n+6))
        result["cases"][name] = item
    return result


def log(record=None):
    if record is None: record = observation()
    return (f"=== RUN   {module.META}\n--- PASS: {module.META} (0.01s)\n=== RUN   {module.RUNTIME}\n"
            f"    fixture.go:1: environment promotion native: {json.dumps(record)}\n--- PASS: {module.RUNTIME} (300.00s)\nPASS\n"
            f"native promotion qualification: final_leakcheck_rc=0 group={module.GROUP}\n"
            f"native promotion qualification: sha={SHA} final_rc=0 group={module.GROUP}\n")


class QualificationTests(unittest.TestCase):
    def test_valid_supplement_does_not_expand_base(self):
        report = module.qualify(log(), SHA)
        self.assertTrue(report["qualified"], report["errors"])
        self.assertFalse(report["admission_enabled"])
        self.assertEqual(report["scope"], "project-environment-promotion-only/promotion-service-graph")

    def test_missing_case_or_proof(self):
        for case in observation()["cases"]:
            record = observation()
            del record["cases"][case]
            self.assertFalse(module.qualify(log(record), SHA)["qualified"])
        for proof in ("private_routing", "destination_secrets", "checkpoint_preserved", "artifact_reused", "cold_without_snapshot", "cold_boot_event", "park_wake"):
            record = observation(); record["cases"]["success"][proof] = False
            self.assertFalse(module.qualify(log(record), SHA)["qualified"], proof)

    def test_mixed_or_incomplete_runtime(self):
        changes = [("backend_deployment_id", uid(999)), ("backend_release_id", uid(998)), ("binding_transport", "http"),
                   ("backend_destination_secret_matches", False), ("backend_instance_id", ""), ("backend_generation", "baseline")]
        for field, value in changes:
            record = observation(); record["cases"]["success"]["recovered_report"][field] = value
            self.assertFalse(module.qualify(log(record), SHA)["qualified"], field)

    def test_reused_admission_and_missing_real_windows(self):
        record = observation(); record["cases"]["success"]["promotion_id"] = record["cases"]["rollback"]["promotion_id"]
        self.assertFalse(module.qualify(log(record), SHA)["qualified"])
        record = observation(); record["cases"]["success"]["healthy_windows"] *= 2
        self.assertFalse(module.qualify(log(record), SHA)["qualified"])
        record = observation(); record["cases"]["rollback"]["regressed_rounds"] = 1
        self.assertFalse(module.qualify(log(record), SHA)["qualified"])

    def test_isolation_is_not_unavailability(self):
        for status in (200, 500, 502, 503):
            record = observation(); record["cases"]["success"]["isolation"]["statuses"]["foreign_tenant"] = status
            self.assertFalse(module.qualify(log(record), SHA)["qualified"])
        record = observation(); record["cases"]["success"]["isolation"]["foreign_account"] = record["cases"]["success"]["isolation"]["owner_account"]
        self.assertFalse(module.qualify(log(record), SHA)["qualified"])

    def test_source_cleanup_and_attribution(self):
        original = log()
        invalid = [original.replace(SHA, "2"*40), original.replace("final_leakcheck_rc=0", "final_leakcheck_rc=1"),
                   original.replace("final_rc=0", "final_rc=1"), original.replace(f"=== RUN   {module.RUNTIME}\n", ""),
                   original.replace(f"--- PASS: {module.RUNTIME}", f"--- SKIP: {module.RUNTIME}"),
                   original + f"--- PASS: {module.RUNTIME} (1s)\n", original + "=== RUN   Unexpected\n--- PASS: Unexpected (1s)\n"]
        for value in invalid: self.assertFalse(module.qualify(value, SHA)["qualified"])

    def test_malformed_records_fail_closed(self):
        for record in (None, [], True, {"schema_version": 1}, dict(observation(), cases=[])):
            value = log().replace(json.dumps(observation()), json.dumps(record))
            self.assertFalse(module.qualify(value, SHA)["qualified"])


if __name__ == "__main__":
    unittest.main()
