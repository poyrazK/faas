import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("run_verdict", Path(__file__).with_name("run-lifecycle-verdict.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class CoverageTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = ["TestSourceRunLifecycleMetal"] + ["TestSourceRunLifecycleMetal/" + case for case in gate.SOURCE_CASES]
        self.host = {"architecture": "x86_64", "kvm": True, "acceptance_marker": True, "machine_id": "host", "boot_id": "boot", "virtualization": "google"}
        self.exits = {name: 0 for name in ("source", "oci", "leakcheck_before", "leakcheck_after")}
        self.write("source", gate.SOURCE, self.source)
        self.write("oci", gate.OCI, gate.OCI_CASES)

    def write(self, name, package, tests, extra=()):
        events = [{"Package": package, "Action": action, "Test": test} for test in tests for action in ("run", "pass")]
        events += list(extra) + [{"Package": package, "Action": "pass"}]
        (self.root / (name + ".jsonl")).write_text("".join(json.dumps(event) + "\n" for event in events))

    def check(self):
        return gate.verdict(self.host, self.root / "source.jsonl", self.root / "oci.jsonl", self.exits)

    def test_complete_virtualized_run_is_functional_only(self):
        result = self.check()
        self.assertTrue(result["functional_pass"])
        self.assertFalse(result["bare_metal_host"])
        self.assertFalse(result["p0_3_complete"])
        self.assertFalse(result["runtime_release_eligibility"])

    def test_file_contract_journey_requires_complete_coverage_and_stays_functional(self):
        tests = ["TestSourceFilesContractsMetal"] + ["TestSourceFilesContractsMetal/" + name for name in gate.FILES_CONTRACT_CASES]
        exits = {"files_contracts": 0, "leakcheck_before": 0, "leakcheck_after": 0}
        self.write("files-contracts", gate.SOURCE, tests)
        result = gate.files_contracts_verdict(self.host, self.root, exits)
        self.assertTrue(result["functional_pass"])
        self.assertFalse(result["runtime_release_eligibility"])
        self.write("files-contracts", gate.SOURCE, tests[:-1])
        with self.assertRaises(ValueError): gate.files_contracts_verdict(self.host, self.root, exits)

    def test_extended_environment_journey_requires_all_cases(self):
        tests = ["TestSourceExtendedEnvironmentMetal"] + ["TestSourceExtendedEnvironmentMetal/" + name for name in gate.EXTENDED_ENVIRONMENT_CASES]
        exits = {"extended_environments": 0, "leakcheck_before": 0, "leakcheck_after": 0}
        self.write("extended-environments", gate.SOURCE, tests)
        result = gate.extended_environment_verdict(self.host, self.root, exits)
        self.assertTrue(result["functional_pass"])
        self.assertFalse(result["runtime_release_eligibility"])
        self.write("extended-environments", gate.SOURCE, tests[:-1])
        with self.assertRaises(ValueError): gate.extended_environment_verdict(self.host, self.root, exits)

    def test_controls_requires_exact_journey_and_zero_leaks(self):
        tests=["TestSourceRunControlsContinuationMetal"]
        exits={"controls":0,"leakcheck_before":0,"leakcheck_after":0}
        self.write("controls",gate.SOURCE,tests)
        result=gate.task_verdict(self.host,self.root,exits,"controls")
        self.assertTrue(result["functional_pass"])
        self.assertFalse(result["runtime_release_eligibility"])
        self.write("controls",gate.SOURCE,["TestSourceTaskContinuationMetal"])
        with self.assertRaises(ValueError):gate.task_verdict(self.host,self.root,exits,"controls")
        self.write("controls",gate.SOURCE,tests,[{"Package":gate.SOURCE,"Action":"skip","Test":tests[0]}])
        with self.assertRaises(ValueError):gate.task_verdict(self.host,self.root,exits,"controls")
        self.write("controls",gate.SOURCE,tests)
        exits["leakcheck_after"]=1
        with self.assertRaises(ValueError):gate.task_verdict(self.host,self.root,exits,"controls")

    def test_system_environment_requires_all_cases_and_clean_exit(self):
        tests = ["TestSourceSystemEnvironmentMetal"] + ["TestSourceSystemEnvironmentMetal/" + name for name in gate.SYSTEM_ENVIRONMENT_CASES]
        exits = {"system_environments": 0, "leakcheck_before": 0, "leakcheck_after": 0}
        self.write("system-environments", gate.SOURCE, tests)
        result = gate.system_environment_verdict(self.host, self.root, exits)
        self.assertTrue(result["functional_pass"])
        self.assertFalse(result["runtime_release_eligibility"])
        self.write("system-environments", gate.SOURCE, tests[:-1])
        with self.assertRaises(ValueError): gate.system_environment_verdict(self.host, self.root, exits)
        self.write("system-environments", gate.SOURCE, tests, [{"Package": gate.SOURCE, "Action": "skip", "Test": tests[-1]}])
        with self.assertRaises(ValueError): gate.system_environment_verdict(self.host, self.root, exits)
        self.write("system-environments", gate.SOURCE, tests)
        exits["leakcheck_after"] = 1
        with self.assertRaises(ValueError): gate.system_environment_verdict(self.host, self.root, exits)

    def test_report_setup_requires_same_report_without_duplicate_physical_work(self):
        cohort = "report-agent-setup"
        self.write(cohort, gate.SOURCE, ["TestDeployedReportAgentMetal", "TestDeployedReportAgentMetal/setup"])
        exits = {"report_agent_setup": 0, "leakcheck_before": 0, "leakcheck_after": 0}
        measurements = {
            "schema": "gregale.report-agent-setup/v1", "reused_report": True,
            "first_setup_to_verified_report_ms": 3000, "first_sample_verification_ms": 500,
            "replay_setup_to_verified_report_ms": 200, "replay_sample_verification_ms": 50,
            "orders": 4, "units": 8, "revenue_cents": 6996, "deployment_count": 1, "request_count": 1,
            "physical_runs": 1, "provider_writes": 1, "provider_reads": 2,
            "identity": {"deployment_id": "deployment", "thread_id": "thread", "pipeline_id": "pipeline",
                         "run_id": "run", "delivery_id": "delivery", "report_bytes": 900, "report_sha256": "a" * 64},
        }
        evidence = self.root / "measurements.json"
        evidence.write_text(json.dumps(measurements))
        self.assertTrue(gate.report_agent_verdict(self.host, self.root, exits, cohort)["functional_pass"])
        for duplicate in ("deployment_count", "request_count", "physical_runs", "provider_writes"):
            with self.subTest(duplicate=duplicate):
                evidence.write_text(json.dumps({**measurements, duplicate: 2}))
                with self.assertRaises(ValueError):
                    gate.report_agent_verdict(self.host, self.root, exits, cohort)

    def test_hosted_report_recovery_requires_live_cursor_and_no_duplicate_runs(self):
        cohort = "report-agent-recovery"
        self.write(cohort, gate.SOURCE, ["TestDeployedReportAgentMetal", "TestDeployedReportAgentMetal/recovery"])
        exits = {"report_agent_recovery": 0, "leakcheck_before": 0, "leakcheck_after": 0}
        evidence = self.root / "measurements.json"
        measurements = {
            "schema": "gregale.report-agent-recovery/v1", "parent_pipeline_id": "parent", "child_pipeline_id": "child",
            "child_request_id": "request", "initial_cursor": 4, "resumed_terminal_cursor": 12, "live_progress": True,
            "merge_failure": "admission_refused", "parent_physical_runs": 2, "rerun_physical_runs": 1,
            "physical_runs_after_replay": 3, "workflow_count": 2, "reused_steps": ["north", "south"],
            "same_uuid_reused_child": True, "lost_ack_recovered": True, "original_receipt_unchanged": True,
            "provider_writes": 1, "provider_reads": 2, "report_bytes": 800, "report_sha256": "a" * 64, "revenue_cents": 6996,
        }
        evidence.write_text(json.dumps(measurements))
        self.assertTrue(gate.report_agent_verdict(self.host, self.root, exits, cohort)["functional_pass"])
        for change in ({"resumed_terminal_cursor": 4}, {"physical_runs_after_replay": 4}, {"reused_steps": ["north"]}):
            with self.subTest(change=change):
                evidence.write_text(json.dumps({**measurements, **change}))
                with self.assertRaises(ValueError):
                    gate.report_agent_verdict(self.host, self.root, exits, cohort)

    def test_empty_or_partial_source_is_refused(self):
        for tests in ([], self.source[:-1]):
            with self.subTest(tests=len(tests)):
                self.write("source", gate.SOURCE, tests)
                with self.assertRaises(ValueError): self.check()

    def test_report_background_requires_dispatcher_retry_and_one_private_copy(self):
        cohort = "report-agent-background"
        self.write(cohort, gate.SOURCE, ["TestSourceHostedBackgroundReportMetal"])
        exits = {"report_agent_background": 0, "leakcheck_before": 0, "leakcheck_after": 0}
        evidence = self.root / "measurements.json"
        measurements = {
            "schema": "gregale.report-agent-background/v1", "app_id": "app", "pipeline_id": "pipeline",
            "request_id": "request", "webhook_delivery_id": "webhook", "artifact_delivery_id": "artifact",
            "receiver_crashed_after_copy": True, "fresh_receiver_recovered": True, "replay_verified": True,
            "dispatcher_attempts": 2, "customer_poll_count": 0, "physical_runs": 3, "workflow_count": 1,
            "provider_writes": 1, "provider_reads": 1, "revenue_cents": 6996, "report_bytes": 381,
            "report_sha256": "a" * 64, "completion_to_copy_ms": 400, "completion_to_download_ready_ms": 4200,
        }
        evidence.write_text(json.dumps(measurements))
        self.assertTrue(gate.report_agent_verdict(self.host, self.root, exits, cohort)["functional_pass"])
        for change in ({"dispatcher_attempts": 1}, {"customer_poll_count": 1}, {"physical_runs": 4},
                       {"provider_writes": 2}, {"fresh_receiver_recovered": False}):
            with self.subTest(change=change):
                evidence.write_text(json.dumps({**measurements, **change}))
                with self.assertRaises(ValueError):
                    gate.report_agent_verdict(self.host, self.root, exits, cohort)

    def test_skip_or_failure_is_refused(self):
        for action in ("skip", "fail"):
            with self.subTest(action=action):
                self.write("source", gate.SOURCE, self.source, [{"Package": gate.SOURCE, "Action": action, "Test": self.source[1]}])
                with self.assertRaises(ValueError): self.check()

    def test_every_process_exit_is_required(self):
        for name in self.exits:
            saved = dict(self.exits)
            self.exits[name] = 1
            with self.subTest(name=name), self.assertRaises(ValueError): self.check()
            self.exits = saved

    def test_oci_interactive_coverage_is_required(self):
        self.write("oci", gate.OCI, gate.OCI_CASES[:-1])
        with self.assertRaises(ValueError): self.check()

    def test_bare_metal_does_not_imply_full_qualification(self):
        self.host["virtualization"] = "none"
        result = self.check()
        self.assertTrue(result["bare_metal_host"])
        self.assertFalse(result["p0_3_complete"])

    def warm_inputs(self):
        cases = ["TestSourceWarmRestoreOOMMetal"] + ["TestSourceWarmRestoreOOMMetal/" + case for case in gate.WARM_OOM_CASES]
        self.write("warm-oom", gate.SOURCE, cases)
        self.write("warm-oom-guest-checks", "github.com/onebox-faas/faas/guest/init", ["TestExecutionPayloadRevokesPristineRestoreFence", "TestResumeWithoutEntropyNacks", "TestServeExecutionOnceHandlesOneRequest"])
        self.write("warm-oom-executor-checks", "github.com/onebox-faas/faas/guest/executor", ["TestGuestOOMCounterRequiresValidEvidence", "TestExecutionOOMNeedsKernelCounterAndKilledProcess"])
        return cases, {"warm_oom": 0, "leakcheck_before": 0, "leakcheck_after": 0}

    def test_warm_oom_complete_is_functional_only(self):
        _, exits = self.warm_inputs()
        result = gate.warm_oom_verdict(self.host, self.root, exits)
        self.assertTrue(result["functional_pass"])
        self.assertFalse(result["bare_metal_host"])
        self.assertFalse(result["p0_3_complete"])
        self.assertFalse(result["runtime_release_eligibility"])

    def test_warm_oom_missing_or_skipped_case_is_refused(self):
        cases, exits = self.warm_inputs()
        self.write("warm-oom", gate.SOURCE, cases[:-1])
        with self.assertRaises(ValueError): gate.warm_oom_verdict(self.host, self.root, exits)
        self.write("warm-oom", gate.SOURCE, cases, [{"Package": gate.SOURCE, "Action": "skip", "Test": cases[1]}])
        with self.assertRaises(ValueError): gate.warm_oom_verdict(self.host, self.root, exits)

    def test_warm_oom_requires_auxiliary_checks_and_clean_exit(self):
        _, exits = self.warm_inputs()
        exits["leakcheck_after"] = 1
        with self.assertRaises(ValueError): gate.warm_oom_verdict(self.host, self.root, exits)

        exits["leakcheck_after"] = 0
        self.write("warm-oom-executor-checks", "github.com/onebox-faas/faas/guest/executor", [])
        with self.assertRaises(ValueError): gate.warm_oom_verdict(self.host, self.root, exits)

    def template_inputs(self):
        warm, _ = self.warm_inputs()
        cases = warm + ["TestSourceTemplateClonesMetal"] + ["TestSourceTemplateClonesMetal/" + case for case in gate.TEMPLATE_CLONE_CASES]
        self.write("template-clones", gate.SOURCE, cases)
        for name, (package, tests) in gate.TEMPLATE_AUXILIARY.items():
            self.write(name, "github.com/onebox-faas/faas/" + package, tests)
        return cases, {"template_clones": 0, "leakcheck_before": 0, "leakcheck_after": 0}

    def test_template_clones_complete_is_functional_only(self):
        _, exits = self.template_inputs()
        result = gate.template_clone_verdict(self.host, self.root, exits)
        self.assertTrue(result["functional_pass"])
        self.assertFalse(result["p0_3_complete"])
        self.assertFalse(result["runtime_release_eligibility"])

    def test_template_clones_requires_every_case_and_auxiliary_package(self):
        cases, exits = self.template_inputs()
        self.write("template-clones", gate.SOURCE, cases[:-1])
        with self.assertRaises(ValueError): gate.template_clone_verdict(self.host, self.root, exits)
        self.template_inputs()
        name, (package, _) = next(iter(gate.TEMPLATE_AUXILIARY.items()))
        self.write(name, "github.com/onebox-faas/faas/" + package, [])
        with self.assertRaises(ValueError): gate.template_clone_verdict(self.host, self.root, exits)

    def test_template_clones_refuses_failed_exit_or_skip(self):
        cases, exits = self.template_inputs()
        exits["leakcheck_after"] = 1
        with self.assertRaises(ValueError): gate.template_clone_verdict(self.host, self.root, exits)
        exits["leakcheck_after"] = 0
        self.write("template-clones", gate.SOURCE, cases, [{"Package": gate.SOURCE, "Action": "skip", "Test": cases[-1]}])
        with self.assertRaises(ValueError): gate.template_clone_verdict(self.host, self.root, exits)


    def native_inputs(self):
        cases, _ = self.template_inputs()
        cases += ["TestSourceNativeRecoveryMetal"] + ["TestSourceNativeRecoveryMetal/" + case for case in gate.NATIVE_RECOVERY_CASES]
        self.write("template-clones", gate.SOURCE, cases,
                   [{"Package": gate.SOURCE, "Action": "output", "Output": "recovery=native_process_recovery_owner_v3\n"} for _ in range(3)])
        package, tests = gate.TEMPLATE_AUXILIARY["template-runtime-checks"]
        self.write("template-runtime-checks", "github.com/onebox-faas/faas/" + package, list(tests) + list(gate.NATIVE_RECOVERY_UNIT_TESTS))
        package, tests = gate.TEMPLATE_AUXILIARY["template-publisher-checks"]
        self.write("template-publisher-checks", "github.com/onebox-faas/faas/" + package, list(tests) + ["TestVMMClientExecutionRestoreWaitsForReconnectWithoutReplaying", "TestVMMClientDestroyWaitsForReconnectWithoutReplaying", "TestExecutionSessionDestroyRequiresConfirmedRetirement"])
        self.write("template-device-checks", "github.com/onebox-faas/faas/pkg/jailsetup", ["TestDeviceScopeStrictDecodingAndReceiptIdentity", "TestDeviceScopeKVMOnlyRequiresAbsentTUN"])
        return cases, {"native_recovery": 0, "leakcheck_before": 0, "leakcheck_after": 0}

    def test_native_recovery_complete_is_functional_only(self):
        _, exits = self.native_inputs()
        result = gate.native_recovery_verdict(self.host, self.root, exits)
        self.assertTrue(result["functional_pass"])
        self.assertEqual(result["source_recovery_profile"], "native_process_recovery_owner_v3")
        self.assertFalse(result["runtime_release_eligibility"])
        self.assertFalse(result["p0_3_complete"])

    def test_native_recovery_requires_every_crash_case(self):
        cases, exits = self.native_inputs()
        self.write("template-clones", gate.SOURCE, cases[:-1])
        with self.assertRaises(ValueError): gate.native_recovery_verdict(self.host, self.root, exits)

    def test_native_recovery_refuses_legacy_profile_and_skip(self):
        cases, exits = self.native_inputs()
        self.write("template-clones", gate.SOURCE, cases)
        with self.assertRaises(ValueError): gate.native_recovery_verdict(self.host, self.root, exits)
        self.native_inputs()
        with (self.root / "template-clones.jsonl").open("a") as stream:
            stream.write(json.dumps({"Package": gate.SOURCE, "Action": "skip", "Test": cases[-1]}) + "\n")
        with self.assertRaises(ValueError): gate.native_recovery_verdict(self.host, self.root, exits)

    def test_native_recovery_requires_owner_regressions_and_all_exits(self):
        _, exits = self.native_inputs()
        self.write("template-runtime-checks", "github.com/onebox-faas/faas/pkg/fcvm", gate.NATIVE_RECOVERY_UNIT_TESTS[:-1])
        with self.assertRaises(ValueError): gate.native_recovery_verdict(self.host, self.root, exits)
        _, exits = self.native_inputs()
        for key in exits:
            failed = dict(exits); failed[key] = 1
            with self.subTest(key=key), self.assertRaises(ValueError): gate.native_recovery_verdict(self.host, self.root, failed)


if __name__ == "__main__":
    unittest.main()
