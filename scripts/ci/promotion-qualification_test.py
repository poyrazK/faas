#!/usr/bin/env python3
"""Exercise fail-closed parsing without making a native qualification claim."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest
import uuid

spec = importlib.util.spec_from_file_location("qualification", Path(__file__).with_name("promotion-qualification.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
SHA = "1" * 40


def uid(index):
    return str(uuid.UUID(int=index))


def control_observation():
    record = dict(schema_version=1, scenario="operator_controls", cleanup=True)
    record["early"] = dict(promotion_id=uid(10), request_id=uuid.UUID(int=11).hex, status="aborted", restart=True,
                           late_probe_fenced=True, candidate_retired=True, graph_config_preserved=True)
    for index, case in enumerate(("restored", "recovered"), 1):
        record[case] = dict(promotion_id=uid(20+index), request_id=uuid.UUID(int=30+index).hex, status="restored", restart=True,
                            healthy_windows=["2026-10-15T10:15:00Z", "2026-10-15T10:16:00Z"],
                            requested_at="2026-10-15T10:14:00Z", deadline="2026-10-15T10:24:00Z",
                            retained_recovered=True, graph_config_restored=True, candidate_pin_preserved=True,
                            artifact_reused=True, retention_preserved=True, candidate_regressed=True, blocked=case == "recovered")
    record["recovered"].update(blocked_request_id=uuid.UUID(int=40).hex, blocked_requested_at="2026-10-15T10:00:00Z",
                               blocked_at="2026-10-15T10:10:05Z", blocked_deadline="2026-10-15T10:10:00Z",
                               blocked_receipt_replayed=True, baseline_retries=5)
    return record


def concurrent_observation():
    record = dict(schema_version=1, scenario="concurrent", fleet_peak=4, account_peak=2, fleet_limit=4, account_limit=2,
                  reclaim_at="2026-10-15T10:02:00Z", previous_lease_until="2026-10-15T10:01:00Z",
                  control_requested_at="2026-10-15T10:03:00Z", jobs=[])
    record.update(dict.fromkeys(("slow_probe", "unrelated_progress_during_delay", "all_progressed_while_active", "owner_restarted", "lease_reclaimed",
                                 "late_observation_fenced", "ordinary_progress_after_control", "control_restored",
                                 "publication_unique", "artifact_reused", "cleanup"), True))
    for index in range(21):
        record["jobs"].append(dict(promotion_id=uid(100+index), account_id=uid(200+index//7), project_id=uid(300+index//7),
                                  environment=f"target-{index%7}", selected_graph=uid(400+index),
                                  healthy_windows=["2026-10-15T10:04:00Z", "2026-10-15T10:05:00Z"],
                                  phase="failed" if index==7 else "succeeded"))
    return record


def admission_observation():
    record = dict(schema_version=1, scenario="admission",
                  limits=dict(fleet_active=24,account_active=8,fleet_queued=128,account_queued=32,wait_seconds=900,health_seconds=600),
                  peaks=dict(ordinary_active=24,ordinary_account=8,total_active=25,total_account=9,queued=128,queued_account=32),
                  waiting=[], waiting_restart_at="2026-10-15T10:01:00Z",cancelled_id=uid(1127),drift_id=uid(1096))
    account = 0
    for index in range(128):
        account = min(index//32,3) if index<127 else 4
        record["waiting"].append(dict(promotion_id=uid(1000+index),account_id=uid(2000+account),queued_at="2026-10-15T10:00:00Z",wait_deadline="2026-10-15T10:15:00Z"))
    record.update(dict.fromkeys(("waiting_restart", "concurrent_admission", "no_waiting_candidates", "account_rejected", "fleet_rejected",
                                "rejections_unaccepted", "fifo", "cancelled_without_start", "drift_conflicted",
                                "drift_preserved", "artifact_reused", "publication_unique", "cleanup"),True))
    record["dispatch"] = dict(promotion_id=uid(1000),queued_at="2026-10-15T10:00:00Z",wait_deadline="2026-10-15T10:15:00Z",
                              started_at="2026-10-15T10:02:00Z",health_deadline="2026-10-15T10:12:00Z",
                              healthy_windows=["2026-10-15T10:03:00Z","2026-10-15T10:04:00Z"],
                              restart_before_preparation=True,idempotent_replay=True,candidate_count=1,deadlines_preserved=True)
    record["recovery"] = dict(promotion_id=uid(3000),request_id=uuid.UUID(int=3004).hex,blocked_request_id=uuid.UUID(int=3005).hex,blocked_requested_at="2026-10-15T10:10:00Z",blocked_deadline="2026-10-15T10:20:00Z",
                              blocked_at="2026-10-15T10:20:05Z",requested_at="2026-10-15T10:21:00Z",deadline="2026-10-15T10:31:00Z",
                              healthy_windows=["2026-10-15T10:22:00Z","2026-10-15T10:23:00Z"],
                              active_before=24,active_after=25,account_after=9,status="restored",selected_before=uid(3001),selected_after=uid(3002),
                              baseline_deployment=uid(3003),restart=True,waiting_preserved=True,ordinary_progress=True,retention_preserved=True,baseline_only=True)
    record["expiry"] = dict(promotion_id=uid(4000),queued_at="2026-10-15T11:00:00Z",wait_deadline="2026-10-15T11:15:00Z",
                            observed_at="2026-10-15T11:15:01Z",reason="queue_timeout",restart=True,no_start=True,baseline_preserved=True)
    return record


def migration_observation(scenario):
    offset = {"migration_success": 0, "migration_rollback": 100, "migration_failure": 200, "migration_recovery": 300}[scenario]
    record = dict(schema_version=1, scenario=scenario, promotion_id=uid(5000), task_id=uid(5001), run_id=uid(5002),
                  baseline_graph=uid(5003), selected_graph=uid(5004), task_count=1, attempts=1, executions=1, source_executions=1,
                  migration_admitted_at="2026-10-15T10:00:00Z", migration_deadline="2026-10-15T10:11:00Z",
                  task_started_at="2026-10-15T10:00:30Z", task_finished_at="2026-10-15T10:01:01Z",
                  exit_code=0, migration_status="succeeded", phase="succeeded")
    record.update(dict.fromkeys(("lost_response", "queued_restart", "admission_disabled", "destination_only", "artifact_reused",
                                "terminal_restart", "schema_preserved", "cleanup"), True))
    for index, key in enumerate(("promotion_id", "task_id", "run_id", "baseline_graph", "selected_graph")):
        record[key] = uid(5000+offset+index)
    if scenario == "migration_failure":
        record.update(exit_code=23, migration_status="failed", phase="failed", selected_graph=record["baseline_graph"], graph_config_preserved=True)
        return record
    record.update(running_restart=True, completed_restart=True, read_only_health=True, preflight_rounds=2, cutover_graph=uid(5004+offset),
                  health_started_at="2026-10-15T10:02:06Z", health_deadline="2026-10-15T10:12:06Z",
                  graph_config_restored=scenario == "migration_rollback")
    if scenario == "migration_rollback":
        record.update(phase="failed", selected_graph=uid(5005+offset), regressed_rounds=2)
    else:
        record["healthy_windows"] = ["2026-10-15T10:03:10Z", "2026-10-15T10:04:10Z"]
    if scenario == "migration_recovery":
        record.update(exit_code=None, migration_status="uncertain")
        record["recovery"] = dict(request_id=uuid.UUID(int=5010).hex, evidence_hash="a"*64, node_id=uid(5011), runtime_id=uid(5012),
                                  lease_token=uid(5013), previous_lease_until="2026-10-15T10:01:00Z", uncertain_at="2026-10-15T10:01:05Z",
                                  requested_at="2026-10-15T10:02:00Z", destroyed_at="2026-10-15T10:02:05Z", resolved_at="2026-10-15T10:02:06Z",
                                  deadline="2026-10-15T10:12:00Z", status="resumed", quiescing_restart=True, competing_rejected=True,
                                  session_gone=True, original_receipt_preserved=True)
    return record


def publication_observation():
    record = dict(schema_version=1, scenario="publication_crashes", cleanup=True)
    points = ("cutover_before_commit", "cutover_after_commit", "rollback_before_commit", "rollback_after_commit")
    for case, offset in (("recovered", 6000), ("newer_intent", 6100)):
        newer = case == "newer_intent"
        item = dict(promotion_id=uid(offset), baseline_graph=uid(offset+1), cutover_graph=uid(offset+2), restored_graph=uid(offset+3),
                    selected_graph=uid(offset+4) if newer else uid(offset+3), config_hash=("b" if newer else "a")*64,
                    phase="rollback_failed" if newer else "failed", publications=3 if newer else 2,
                    candidate_jobs=5, newer_intent_preserved=newer, events=[])
        item.update(dict.fromkeys(("candidate_pins_preserved", "completed_jobs_not_replayed", "artifact_reused", "graph_config_consistent", "terminal_restart"), True))
        for index, point in enumerate(points):
            if newer and index != 3: continue
            minute = 2*index
            selected = item["baseline_graph"] if index == 0 else item["restored_graph"] if index == 3 else item["cutover_graph"]
            item["events"].append(dict(point=point, promotion_id=item["promotion_id"], release_id=selected if index in (1,3) else uid(offset+10+index),
                                       selected_graph=selected, pid=offset+index, observed_at=f"2026-10-15T10:{minute:02d}:00Z",
                                       killed_at=f"2026-10-15T10:{minute:02d}:01Z", restarted_at=f"2026-10-15T10:{minute:02d}:02Z",
                                       barrier_deadline=f"2026-10-15T10:{minute:02d}:45Z",
                                       phase=("cutover", "verifying", "rolling_back", "rolling_back")[index], generation=(2,3,6,6)[index],
                                       regressed_rounds=2 if index>=2 else 0,
                                       deadline="2026-10-15T10:10:00Z" if index == 0 else "2026-10-15T10:12:00Z",
                                       committed=index in (1,3), checkpoint_preserved=True))
        record[case] = item
    return record


def observation(scenario):
    if scenario == "publication_crashes": return publication_observation()
    if scenario.startswith("migration_"): return migration_observation(scenario)
    if scenario == "operator_controls": return control_observation()
    if scenario == "concurrent": return concurrent_observation()
    if scenario == "admission": return admission_observation()
    return dict(schema_version=1, scenario=scenario,
                promotion_id="11111111-1111-4111-8111-111111111111",
                retry_release_id="22222222-2222-4222-8222-222222222222",
                retry_deployment_id="33333333-3333-4333-8333-333333333333",
                candidate_release_id="44444444-4444-4444-8444-444444444444",
                preflight_rounds=2, candidate_jobs=20, retry_attempts=2,
                healthy_windows=["2026-10-15T10:01:00Z", "2026-10-15T10:02:00Z"],
                regressed_rounds=2 if scenario == "rolled_back" else 0,
                graph_config_restored=scenario == "rolled_back",
                **dict.fromkeys(module.PROOFS, True))


def log(records=None, group="all"):
    records = records if records is not None else {name: observation(scenario) for name, scenario in module.SCENARIOS.items()}
    lines = []
    for selected in (module.GROUPS if group == "all" else [group]):
        required = module.GROUPS[selected]
        if module.META_TEST in required:
            lines += [f"=== RUN   {module.META_TEST}", f"--- PASS: {module.META_TEST} (0.01s)"]
        for name, record in records.items():
            if name in required:
                lines += [f"=== RUN   {name}", "    fixture.go:1: environment promotion native: " + json.dumps(record), f"--- PASS: {name} (300.00s)"]
        lines += ["PASS", f"native promotion qualification: final_leakcheck_rc=0 group={selected}",
                  f"native promotion qualification: sha={SHA} final_rc=0 group={selected}"]
    return "\n".join(lines) + "\n"


class QualificationTests(unittest.TestCase):
    def test_rollout_requires_the_current_complete_native_suite(self):
        import re
        source = Path(__file__).resolve().parents[2] / "pkg/promotionrollout/evidence.go"
        names = set(re.findall(r'"(TestProjectEnvironmentPromotion\w+)"', source.read_text()))
        self.assertEqual(names, module.REQUIRED_TESTS)

    def test_complete(self):
        report = module.qualify(log(), SHA)
        self.assertTrue(report["qualified"], report["errors"])
        self.assertFalse(report["admission_enabled"])

    def test_incomplete_and_failed_runs(self):
        good = log()
        first = next(iter(module.SCENARIOS))
        variants = ["", good.replace(f"--- PASS: {first}", f"--- SKIP: {first}"),
                    good.replace(f"--- PASS: {first}", f"--- FAIL: {first}"),
                    good.replace("final_leakcheck_rc=0", "final_leakcheck_rc=1"),
                    good.replace(f"sha={SHA}", "sha=" + "2" * 40),
                    good + f"--- PASS: {first} (0s)\n", good + "panic: timed out\n",
                    good + "native promotion qualification: final_leakcheck_rc=1\n",
                    good + f"native promotion qualification: sha={SHA} final_rc=1\n",
                    good + "=== RUN   Unfinished\n",
                    good.replace(f"--- PASS: {module.META_TEST} (0.01s)\n", ""),
                    good + "    --- SKIP: child (0s)\n",
                    good.replace("environment promotion native:", "missing evidence:", 1),
                    good.replace("environment promotion native: {", "environment promotion native: malformed {", 1)]
        for candidate in variants:
            with self.subTest(candidate=candidate[-100:]):
                self.assertFalse(module.qualify(candidate, SHA)["qualified"])
        self.assertFalse(module.qualify(good, "invalid")["qualified"])

    def test_every_required_proof(self):
        records = {name: observation(scenario) for name, scenario in module.SCENARIOS.items()}
        for name, record in records.items():
            for key in record:
                changed = copy.deepcopy(records)
                del changed[name][key]
                # Healthy windows and regression rounds have scenario-specific meaning.
                if key == "healthy_windows" and record["scenario"] == "rolled_back" or key == "regressed_rounds" and record["scenario"] == "succeeded":
                    continue
                with self.subTest(name=name, key=key):
                    self.assertFalse(module.qualify(log(changed), SHA)["qualified"])

    def test_partitions_are_individually_scoped(self):
        for group in module.GROUPS:
            report=module.qualify(log(group=group),SHA,group)
            self.assertTrue(report["qualified"],report["errors"])
            self.assertEqual(report["scope"],"project-environment-promotion-only/"+group)
            self.assertFalse(module.qualify(log(group=group),SHA)["qualified"])
            for other in module.GROUPS:
                if other!=group:
                    self.assertFalse(module.qualify(log(group=other),SHA,group)["qualified"])

    def test_nested_control_requirements(self):
        name="TestProjectEnvironmentPromotionControlsMetal"
        for case, item in control_observation().items():
            if not isinstance(item,dict):continue
            for key in item:
                records={key:observation(value) for key,value in module.SCENARIOS.items()}
                del records[name][case][key]
                with self.subTest(case=case,key=key):
                    self.assertFalse(module.qualify(log(records),SHA)["qualified"])

    def test_publication_requires_every_nested_observation(self):
        name = "TestProjectEnvironmentPromotionPublicationMetal"
        original = publication_observation()
        for case in ("recovered", "newer_intent"):
            paths = [(case, key) for key in original[case]]
            paths += [(case, "events", index, key) for index, event in enumerate(original[case]["events"]) for key in event]
            for path in paths:
                records = {key:observation(value) for key,value in module.SCENARIOS.items()}
                node = records[name]
                for part in path[:-1]: node = node[part]
                del node[path[-1]]
                with self.subTest(path=path):
                    self.assertFalse(module.qualify(log(records), SHA)["qualified"])

    def test_publication_rejects_inconsistent_crash_or_recovery(self):
        name = "TestProjectEnvironmentPromotionPublicationMetal"
        changes = [(("recovered", "publications"), 3), (("recovered", "candidate_jobs"), True),
                   (("recovered", "selected_graph"), uid(6002)), (("newer_intent", "selected_graph"), uid(6103)),
                   (("newer_intent", "newer_intent_preserved"), False), (("newer_intent", "phase"), "failed"),
                   (("newer_intent", "config_hash"), "a"*64),
                   (("recovered", "events", 0, "release_id"), uid(6001)),
                   (("recovered", "events", 1, "committed"), False),
                   (("recovered", "events", 1, "pid"), 6000),
                   (("recovered", "events", 1, "point"), "cutover_before_commit"),
                   (("recovered", "events", 1, "generation"), 2),
                   (("recovered", "events", 2, "deadline"), "2026-10-15T10:13:00Z"),
                   (("recovered", "events", 2, "promotion_id"), uid(6100)),
                   (("recovered", "events", 3, "observed_at"), "2026-10-15T10:03:00Z"),
                   (("recovered", "events", 0, "killed_at"), "2026-10-15T10:00:46Z"),
                   (("recovered", "events", 0, "barrier_deadline"), "2026-10-15T10:00:01Z"),
                   (("recovered", "events", 1, "checkpoint_preserved"), False)]
        for path, value in changes:
            records = {key:observation(value) for key,value in module.SCENARIOS.items()}
            node = records[name]
            for part in path[:-1]: node = node[part]
            node[path[-1]] = value
            with self.subTest(path=path, value=value):
                self.assertFalse(module.qualify(log(records), SHA)["qualified"])
        records = {key:observation(value) for key,value in module.SCENARIOS.items()}
        records[name]["recovered"]["events"].pop()
        self.assertFalse(module.qualify(log(records), SHA)["qualified"])

    def test_load_and_recovery_reject_false_evidence(self):
        changes=[("concurrent", "fleet_peak", True), ("concurrent", "account_peak", 3),
                 ("concurrent", "fleet_limit", 5), ("concurrent", "jobs", concurrent_observation()["jobs"][:20]),
                 ("concurrent", "reclaim_at", "2026-10-15T10:00:00Z"),
                 ("operator_controls", "recovered", dict(control_observation()["recovered"],blocked_at="2026-10-15T10:09:59Z")),
                 ("operator_controls", "recovered", dict(control_observation()["recovered"],baseline_retries=True)),
                 ("operator_controls", "restored", dict(control_observation()["restored"],healthy_windows=["2026-10-15T10:00:00Z","2026-10-15T10:01:00Z"]))]
        for scenario,field,value in changes:
            records={key:observation(value) for key,value in module.SCENARIOS.items()}
            name=next(key for key,value in module.SCENARIOS.items() if value==scenario)
            records[name][field]=value
            with self.subTest(scenario=scenario,field=field):
                self.assertFalse(module.qualify(log(records),SHA)["qualified"])
        for field in concurrent_observation()["jobs"][0]:
            records={key:observation(value) for key,value in module.SCENARIOS.items()}
            del records["TestProjectEnvironmentPromotionConcurrentMetal"]["jobs"][0][field]
            with self.subTest(field=field):
                self.assertFalse(module.qualify(log(records),SHA)["qualified"])
        records={key:observation(value) for key,value in module.SCENARIOS.items()}
        jobs=records["TestProjectEnvironmentPromotionConcurrentMetal"]["jobs"]
        jobs[1]["promotion_id"]=jobs[0]["promotion_id"]
        self.assertFalse(module.qualify(log(records),SHA)["qualified"])


    def test_migration_rejects_missing_recovery_evidence(self):
        name = "TestProjectEnvironmentPromotionMigrationRecoveryMetal"
        for key in migration_observation("migration_recovery")["recovery"]:
            records = {key:observation(value) for key,value in module.SCENARIOS.items()}
            del records[name]["recovery"][key]
            with self.subTest(key=key):
                self.assertFalse(module.qualify(log(records), SHA)["qualified"])

    def test_migration_rejects_duplicate_execution_and_forged_results(self):
        changes = [("executions", 2), ("source_executions", 2), ("attempts", True), ("task_count", 2),
                   ("task_id", uid(5000)), ("task_started_at", "2026-10-15T10:01:00Z"),
                   ("migration_deadline", "2026-10-15T10:10:00Z"), ("task_finished_at", "2026-10-15T09:59:00Z"),
                   ("health_started_at", "2026-10-15T10:00:00Z"), ("health_deadline", "2026-10-15T10:11:00Z"),
                   ("healthy_windows", ["2026-10-15T10:03:00Z"]*2), ("selected_graph", uid(5003)),
                   ("exit_code", False), ("migration_status", "uncertain"), ("phase", "failed")]
        for field, value in changes:
            records = {key:observation(value) for key,value in module.SCENARIOS.items()}
            records["TestProjectEnvironmentPromotionMigrationSuccessMetal"][field] = value
            with self.subTest(field=field):
                self.assertFalse(module.qualify(log(records), SHA)["qualified"])
        changes = [("destroyed_at", "2026-10-15T10:01:59Z"), ("uncertain_at", "2026-10-15T10:00:59Z"),
                   ("deadline", "2026-10-15T10:03:00Z"), ("runtime_id", []), ("status", "resolved"), ("evidence_hash", "private")]
        for field, value in changes:
            records = {key:observation(value) for key,value in module.SCENARIOS.items()}
            records["TestProjectEnvironmentPromotionMigrationRecoveryMetal"]["recovery"][field] = value
            self.assertFalse(module.qualify(log(records), SHA)["qualified"])
        for value in (0, False, 23):
            records = {key:observation(value) for key,value in module.SCENARIOS.items()}
            records["TestProjectEnvironmentPromotionMigrationRecoveryMetal"]["exit_code"] = value
            self.assertFalse(module.qualify(log(records), SHA)["qualified"])
        for field in ("promotion_id", "task_id", "run_id"):
            records = {key:observation(value) for key,value in module.SCENARIOS.items()}
            records["TestProjectEnvironmentPromotionMigrationRecoveryMetal"][field] = records["TestProjectEnvironmentPromotionMigrationSuccessMetal"][field]
            self.assertFalse(module.qualify(log(records), SHA)["qualified"])

    def test_admission_rejects_missing_nested_evidence(self):
        name="TestProjectEnvironmentPromotionAdmissionMetal"
        for section in ("dispatch","recovery","expiry","limits","peaks"):
            for key in admission_observation()[section]:
                records={key:observation(value) for key,value in module.SCENARIOS.items()}
                del records[name][section][key]
                with self.subTest(section=section,key=key):
                    self.assertFalse(module.qualify(log(records),SHA)["qualified"])
        for key in admission_observation()["waiting"][0]:
            records={key:observation(value) for key,value in module.SCENARIOS.items()}
            del records[name]["waiting"][0][key]
            self.assertFalse(module.qualify(log(records),SHA)["qualified"])

    def test_admission_rejects_false_limits_and_timing(self):
        name="TestProjectEnvironmentPromotionAdmissionMetal"
        changes=[("limits","fleet_active",25),("limits","wait_seconds",60),("peaks","ordinary_active",True),
                 ("peaks","total_active",24),("dispatch","candidate_count",2),
                 ("dispatch","started_at","2026-10-15T09:59:59Z"),
                 ("dispatch","health_deadline","2026-10-15T10:11:59Z"),
                 ("dispatch","healthy_windows",["2026-10-15T10:02:00Z"]*2),
                 ("recovery","active_before",23),("recovery","active_after",24),
                 ("recovery","blocked_at","2026-10-15T10:19:59Z"),
                 ("recovery","healthy_windows",["2026-10-15T10:15:00Z","2026-10-15T10:16:00Z"]),
                 ("expiry","observed_at","2026-10-15T11:14:59Z"),
                 ("expiry","wait_deadline","2026-10-15T11:01:00Z")]
        for section,key,value in changes:
            records={key:observation(value) for key,value in module.SCENARIOS.items()}
            records[name][section][key]=value
            with self.subTest(section=section,key=key):
                self.assertFalse(module.qualify(log(records),SHA)["qualified"])
        for mutate in (lambda w:w.pop(),lambda w:w.__setitem__(1,copy.deepcopy(w[0])),lambda w:w[0].__setitem__("wait_deadline","2026-10-15T10:20:00Z")):
            records={key:observation(value) for key,value in module.SCENARIOS.items()}
            mutate(records[name]["waiting"])
            self.assertFalse(module.qualify(log(records),SHA)["qualified"])


    def test_admission_malformed_types_fail_closed(self):
        name="TestProjectEnvironmentPromotionAdmissionMetal"
        for key,value in [("waiting",True),("cancelled_id",{}),("drift_id",[]),("dispatch",None),("recovery",[]),("expiry",True),("peaks",True),("waiting_restart_at",None)]:
            records={key:observation(value) for key,value in module.SCENARIOS.items()}
            records[name][key]=value
            with self.subTest(key=key):
                self.assertFalse(module.qualify(log(records),SHA)["qualified"])

    def test_invalid_observations(self):
        name = next(iter(module.SCENARIOS))
        for field, value in [("preflight_rounds", True), ("candidate_jobs", 0), ("retry_attempts", 1),
                             ("cleanup", "true"), ("promotion_id", ""),
                             ("retry_release_id", "44444444-4444-4444-8444-444444444444"),
                             ("healthy_windows", ["2026-10-15T10:01:00Z"] * 2),
                             ("healthy_windows", ["2026-10-15T10:01:00", "2026-10-15T10:02:00"]),
                             ("healthy_windows", [None, None])]:
            records = {key: observation(scenario) for key, scenario in module.SCENARIOS.items()}
            records[name][field] = value
            with self.subTest(field=field, value=value):
                self.assertFalse(module.qualify(log(records), SHA)["qualified"])


if __name__ == "__main__":
    unittest.main()
