#!/usr/bin/env python3
"""Verify complete real-VM Run coverage; never mint runtime release eligibility."""
import argparse
import hashlib
import json
import re
from pathlib import Path

SOURCE = "github.com/onebox-faas/faas/pkg/e2etest/runlifecycle"
OCI = "github.com/onebox-faas/faas/cmd/e2e"
SOURCE_CASES = (
    "source_result_artifact_and_usage", "apid_restart_replays_original_receipt",
    "fresh_guest_and_network_isolation", "guest_error_is_terminal", "output_is_bounded",
    "running_cancel_tears_down", "deadline_does_not_extend", "queued_expiry_never_dispatches",
    "schedd_restart_never_reexecutes_running_work", "vmmd_restart_never_reexecutes_running_work",
    "payload_erasure_and_single_usage_entry",
)
OCI_CASES = (
    "TestImageRunsMetal", "TestImageRunProcessMetal", "TestInteractiveImageRunMetal",
    "TestInteractiveImageRunMetal/pipes", "TestInteractiveImageRunMetal/tty",
    "TestInteractiveImageRunMetal/disconnect_cancels_the_run",
)
WARM_OOM_CASES = (
    "cold_real_oom", "payload_free_capture", "repeated_restores_are_clean",
    "warm_real_oom_and_recovery", "corrupt_memory_cold_fallback",
    "corrupt_vmstate_cold_fallback", "payload_erasure_and_single_usage_entry",
)
TEMPLATE_CLONE_CASES = (
    "automatic_capture_and_publication", "idempotent_publication_after_restart",
    "failed_capture_never_publishes", "catalog_retention_and_orphan_gc",
    "four_live_clones_are_isolated", "cancelling_one_clone_spares_its_peer",
    "corrupt_drive_cold_fallback", "corrupt_backing_cold_fallback",
    "template_unchanged_and_usage_settled",
)
NATIVE_RECOVERY_CASES = (
    "crash_before_boot", "crash_after_pristine_ack", "crash_after_pause", "crash_during_publication", "crash_after_capture",
    "schedd_crash_during_capture", "published_template_survives_owner_restart",
    "vmmd_restart_never_reexecutes_running_work", "schedd_restart_never_reexecutes_running_work",
    "payload_erasure_and_single_usage_entry",
)
FILES_CONTRACT_CASES = (
    "prepare_and_missing_input_never_admits", "verified_upload_is_immutable_and_family_scoped",
    "cold_large_file_roundtrip_and_valid_contract", "warm_clones_get_independent_input_files",
    "contract_failure_preserves_result_without_reexecution", "cli_streams_local_files_and_downloads_stored_outputs",
    "revocation_and_apid_restart_keep_admission_replay", "payload_erasure_single_usage_and_no_live_resources",
)
EXTENDED_ENVIRONMENT_CASES = (
    "unready_environment_and_recipe_reuse", "isolated_builder_and_provenance_publication", "python_hash_pinned_wheel_build_and_import",
    "dependency_import_and_fresh_vm_isolation", "long_run_does_not_block_standard_lane",
    "five_minute_deadline_and_cancel_release_capacity", "retirement_preserves_admitted_run_then_collects",
    "completion_deliveries_survive_scheduler_restart", "payload_erasure_single_usage_and_no_live_resources",
)
NATIVE_RECOVERY_UNIT_TESTS = (
    "TestExecutionTemplateNativeAuthorityDoesNotWidenOrdinaryCapture",
    "TestExecutionTemplateNativeFlightFencesLaunchAndRequiresRetirement",
    "TestExecutionTemplateNativeRecoveryRequiresDeadOriginalAndPreservesObjects",
    "TestExecutionTemplateNativeFlightRejectsCorruptionAndReplacement",
    "TestExecutionTemplateNativeModeRemainsOptIn",
    "TestNativeRuntimeCaptureJournalStartsCompletesAndNeverReplays",
    "TestNativeRuntimeCaptureAuthorityRequiresAFreshCoupledNamespace",
    "TestNativeSnapshotCapturePermitChecksTheDurableRuntimeStart",
    "TestManagerRequestsNativeRuntimeCaptureOnlyForOrdinaryCaptures",
    "TestNativeLaunchOwnerProvesEarlierGenerationExit",
    "TestNativeJailDeviceNetworklessScopeCannotBorrowTUNAuthority",
    "TestNativeImageInventoryLoadsAnEpochPublishedAfterItsDirectorySnapshot",
)
TEMPLATE_AUXILIARY = {
    "template-publisher-checks": ("pkg/sched", (
        "TestExecutionTemplatePublicationIsCompleteAndIdempotent", "TestRuntimeSnapshotBundleAuthenticatesEveryObject",
        "TestExecutionTemplatePartialCaptureCannotPublish", "TestExecutionTemplateLostAcknowledgementKeepsTheWinner",
        "TestExecutionTemplateUnknownCommitRetainsArtifacts", "TestExecutionTemplatePublicationRaceRetiresOnlyItsLoser",
        "TestRuntimeSnapshotReaderIsReadOnlyAndPrefersNative")),
    "template-runtime-checks": ("pkg/fcvm", ("TestExecutionTemplateCannotAcceptAnExecution", "TestExecutionTemplatePrivatePublicationCannotOverwriteOrEscape", "TestAdoptionStateInventoryClassifiesEveryInstanceField", "TestAdoptionStateInventoryClassifiesEveryMap")),
    "template-auth-checks": ("pkg/vmmdgrpc", ("TestCallerPolicyCoversEveryMethod", "TestCallerPolicyAllowList")),
    "template-wiring-checks": ("cmd/schedd", ("TestExecutionTemplateShapesFailClosed",)),
    "template-retention-checks": ("pkg/imaged", ("TestExecutionTemplateGCProtectsCatalogAndReclaimsOnlyOldOrphans",)),
}


def validate_host(host):
    if host.get("architecture") != "x86_64" or host.get("kvm") is not True or host.get("acceptance_marker") is not True:
        raise ValueError("missing designated x86_64 KVM host evidence")
    if not host.get("machine_id") or not host.get("boot_id"):
        raise ValueError("host identity is missing")


def coverage(path, package, required):
    ran, passed, package_pass = set(), set(), False
    with Path(path).open() as stream:
        for line in stream:
            event = json.loads(line)
            if event.get("Package") != package:
                raise ValueError("unexpected test package in evidence")
            action, test = event.get("Action"), event.get("Test")
            if action in ("fail", "skip"):
                raise ValueError(f"{package}: {action}: {test or 'package'}")
            if action == "run" and test:
                ran.add(test)
            if action == "pass":
                if test:
                    passed.add(test)
                else:
                    package_pass = True
    missing = set(required) - (ran & passed)
    if missing or not package_pass:
        raise ValueError(f"incomplete {package} coverage: {sorted(missing)}")
    return sorted(passed)


def verdict(host, source_log, oci_log, exits):
    if any(exits.get(name) != 0 for name in ("source", "oci", "leakcheck_before", "leakcheck_after")):
        raise ValueError("tests and both leakchecks must exit zero")
    source = coverage(source_log, SOURCE, ["TestSourceRunLifecycleMetal"] + ["TestSourceRunLifecycleMetal/" + name for name in SOURCE_CASES])
    oci = coverage(oci_log, OCI, OCI_CASES)
    validate_host(host)
    return {
        "schema": "gregale.run-lifecycle-functional/v1", "functional_pass": True,
        "bare_metal_host": host.get("virtualization") == "none",
        # The matrix intentionally covers cold source execution and OCI sessions.
        # Sanitized snapshot restore and every language/profile still need evidence.
        "p0_3_complete": False, "runtime_release_eligibility": False,
        "host": host, "source_tests": source, "oci_tests": oci,
        "source_recovery_profile": "journal_adoption_v7",
        "limits": ["source: node24 standard cold boot only", "fixture scan is not vulnerability evidence",
                   "sanitized source snapshot restore, OOM and other runtime/profile cohorts remain unqualified"],
    }


def warm_oom_verdict(host, root, exits, logfile="warm-oom"):
    validate_host(host)
    if any(exits.get(name) != 0 for name in ("warm_oom", "leakcheck_before", "leakcheck_after")):
        raise ValueError("warm/OOM tests and both leakchecks must exit zero")
    tests = coverage(root / (logfile + ".jsonl"), SOURCE,
                     ["TestSourceWarmRestoreOOMMetal"] + ["TestSourceWarmRestoreOOMMetal/" + case for case in WARM_OOM_CASES])
    guest = coverage(root / "warm-oom-guest-checks.jsonl", "github.com/onebox-faas/faas/guest/init",
                     ["TestExecutionPayloadRevokesPristineRestoreFence", "TestResumeWithoutEntropyNacks", "TestServeExecutionOnceHandlesOneRequest"])
    executor = coverage(root / "warm-oom-executor-checks.jsonl", "github.com/onebox-faas/faas/guest/executor",
                        ["TestGuestOOMCounterRequiresValidEvidence", "TestExecutionOOMNeedsKernelCounterAndKilledProcess"])
    return {"schema": "gregale.run-warm-oom-functional/v1", "cohort": "warm-oom", "functional_pass": True,
            "bare_metal_host": host.get("virtualization") == "none", "p0_3_complete": False,
            "runtime_release_eligibility": False, "host": host, "tests": tests,
            "guest_tests": guest, "executor_tests": executor,
            "source_recovery_profile": "journal_adoption_v7",
            "limits": ["node24 standard 256 MiB / 64 MiB scratch only", "sequential restore isolation only",
                       "production publisher captures a new guest before any Execute RPC; background warmup and concurrency require the template-clones cohort",
                       "fixture scan is not vulnerability evidence", "other runtime/profile cohorts and full native release qualification remain open"]}


def template_clone_verdict(host, root, exits):
    if any(exits.get(name) != 0 for name in ("template_clones", "leakcheck_before", "leakcheck_after")):
        raise ValueError("template/clone tests and both leakchecks must exit zero")
    result = warm_oom_verdict(host, root, {"warm_oom": exits["template_clones"], "leakcheck_before": exits["leakcheck_before"], "leakcheck_after": exits["leakcheck_after"]}, "template-clones")
    result["tests"] = coverage(root / "template-clones.jsonl", SOURCE,
                               ["TestSourceTemplateClonesMetal"] + ["TestSourceTemplateClonesMetal/" + case for case in TEMPLATE_CLONE_CASES])
    result["auxiliary_tests"] = {name: coverage(root / (name + ".jsonl"), "github.com/onebox-faas/faas/" + package, tests)
                                 for name, (package, tests) in TEMPLATE_AUXILIARY.items()}
    result.update(schema="gregale.run-template-clones-functional/v1", cohort="template-clones")
    result["limits"] = ["node24 standard 256 MiB / 64 MiB scratch only", "four simultaneous same-node networkless clones only",
                        "local artifact backend only", "production publisher code uses operator-provided release pins and a dedicated platform account",
                        "fixture scan is not image provenance or vulnerability evidence", "cross-node/CPU-cohort behavior, other profiles, full billing and native release qualification remain open"]
    return result


def native_recovery_verdict(host, root, exits):
    if any(exits.get(name) != 0 for name in ("native_recovery", "leakcheck_before", "leakcheck_after")):
        raise ValueError("native recovery tests and both leakchecks must exit zero")
    mapped = {"template_clones": exits["native_recovery"], "leakcheck_before": exits["leakcheck_before"], "leakcheck_after": exits["leakcheck_after"]}
    result = template_clone_verdict(host, root, mapped)
    result["tests"] = coverage(root / "template-clones.jsonl", SOURCE,
                               ["TestSourceNativeRecoveryMetal"] + ["TestSourceNativeRecoveryMetal/" + case for case in NATIVE_RECOVERY_CASES])
    result["native_runtime_tests"] = coverage(root / "template-runtime-checks.jsonl", "github.com/onebox-faas/faas/pkg/fcvm", NATIVE_RECOVERY_UNIT_TESTS)
    result["native_scheduler_tests"] = coverage(root / "template-publisher-checks.jsonl", "github.com/onebox-faas/faas/pkg/sched", ("TestVMMClientExecutionRestoreWaitsForReconnectWithoutReplaying", "TestVMMClientDestroyWaitsForReconnectWithoutReplaying", "TestExecutionSessionDestroyRequiresConfirmedRetirement"))
    result["native_device_tests"] = coverage(root / "template-device-checks.jsonl", "github.com/onebox-faas/faas/pkg/jailsetup", ("TestDeviceScopeStrictDecodingAndReceiptIdentity", "TestDeviceScopeKVMOnlyRequiresAbsentTUN"))
    events = [json.loads(line) for line in (root / "template-clones.jsonl").read_text().splitlines()]
    output = "".join(event.get("Output", "") for event in events if event.get("Action") == "output")
    if output.count("recovery=native_process_recovery_owner_v3") != 3 or "recovery=journal_adoption_v7" in output:
        raise ValueError("all three fixtures must attest native recovery with version-3 launch owners")
    result.update(schema="gregale.run-native-recovery-functional/v1", cohort="native-recovery", source_recovery_profile="native_process_recovery_owner_v3")
    result["limits"] += ["native_runtime_capture and native_launch_owner_records are required",
                         "interrupted captures are retired, never promoted or replayed; incomplete native capture journals remain audit evidence",
                         "host reboot, cross-node recovery, rollback to older readers, other profiles and production image provenance remain separate gates"]
    return result


def files_contracts_verdict(host, root, exits):
    validate_host(host)
    if any(exits.get(name) != 0 for name in ("files_contracts", "leakcheck_before", "leakcheck_after")):
        raise ValueError("file/contract journey and both leakchecks must exit zero")
    tests = coverage(root / "files-contracts.jsonl", SOURCE,
        ["TestSourceFilesContractsMetal"] + ["TestSourceFilesContractsMetal/" + name for name in FILES_CONTRACT_CASES])
    return {"schema": "gregale.run-files-contracts-functional/v1", "cohort": "files-contracts", "functional_pass": True,
            "bare_metal_host": host.get("virtualization") == "none", "p0_3_complete": False,
            "runtime_release_eligibility": False, "host": host, "tests": tests,
            "limitations": ["functional evidence on the selected fixture only", "preview writer remains operator gated",
                            "fixture scan is not runtime provenance or vulnerability evidence",
                            "other runtimes/profiles and native production release qualification remain open"]}


def extended_environment_verdict(host, root, exits):
    validate_host(host)
    if any(exits.get(name) != 0 for name in ("extended_environments", "leakcheck_before", "leakcheck_after")):
        raise ValueError("extended/environment journey and both leakchecks must exit zero")
    tests = coverage(root / "extended-environments.jsonl", SOURCE,
        ["TestSourceExtendedEnvironmentMetal"] + ["TestSourceExtendedEnvironmentMetal/" + name for name in EXTENDED_ENVIRONMENT_CASES])
    return {"schema": "gregale.run-extended-environments-functional/v1", "cohort": "extended-environments", "functional_pass": True,
            "bare_metal_host": host.get("virtualization") == "none", "runtime_release_eligibility": False,
            "host": host, "tests": tests,
            "limitations": ["node24 npm and python313 binary wheel recipes; local storage only", "functional evidence on operator-selected Google KVM host",
                "platform runtime base uses an explicit test-only scan fixture; environment publication uses exact-byte Grype scan",
                "other runtime/profile recipes and native production release eligibility remain separate gates"]}



SYSTEM_ENVIRONMENT_CASES = (
    "versioned_system_recipe_and_immutable_reuse", "isolated_system_install_and_scanned_publication",
    "pdf_task_stored_report_contract_and_replay", "retirement_and_single_usage_cleanup",
)

def system_environment_verdict(host, root, exits):
    validate_host(host)
    if any(exits.get(name) != 0 for name in ("system_environments", "leakcheck_before", "leakcheck_after")):
        raise ValueError("system environment journey and both leakchecks must exit zero")
    tests = coverage(root / "system-environments.jsonl", SOURCE,
        ["TestSourceSystemEnvironmentMetal"] + ["TestSourceSystemEnvironmentMetal/" + name for name in SYSTEM_ENVIRONMENT_CASES])
    return {"schema": "gregale.run-system-environments-functional/v1", "cohort": "system-environments", "functional_pass": True,
            "bare_metal_host": host.get("virtualization") == "none", "runtime_release_eligibility": False,
            "host": host, "tests": tests,
            "limitations": ["one pinned Wolfi Python313/Poppler recipe; local retained files",
                           "platform boot bases use test-only scan fixtures; custom environment requires real Grype scan",
                           "native production image eligibility, other package combinations and private registries remain separate"]}


def report_agent_verdict(host, root, exits, cohort):
    validate_host(host)
    if any(exits.get(name) != 0 for name in (cohort.replace("-", "_"), "leakcheck_before", "leakcheck_after")):
        raise ValueError("Report Agent journey and both leakchecks must exit zero")
    tests = {
        "report-agent-json": "TestSourceHostedReportAgentMetal",
        "report-agent-files": "TestSourceScopedReportFileMetal",
        "report-agent-hosted": "TestDeployedReportAgentMetal",
        "report-agent-setup": "TestDeployedReportAgentMetal",
        "report-agent-recovery": "TestDeployedReportAgentMetal",
        "report-agent-background": "TestSourceHostedBackgroundReportMetal",
    }
    required = [tests[cohort]]
    if cohort == "report-agent-hosted":
        required.extend(tests[cohort] + "/" + name for name in ("json", "csv"))
    if cohort == "report-agent-setup":
        required.append(tests[cohort] + "/setup")
    if cohort == "report-agent-recovery":
        required.append(tests[cohort] + "/recovery")
    completed = coverage(root / (cohort + ".jsonl"), SOURCE, required)
    limitations = {
        "report-agent-json": ["worker processes consume the persisted inbox through the test driver",
                              "fixture workload identity and isolated S3 wire endpoint"],
        "report-agent-files": ["one CSV processed by the Report Agent's delegated sales task"],
        "report-agent-hosted": ["isolated PostgreSQL and S3-compatible provider fixture",
                                "no provider invoices or production runtime image qualification"],
        "report-agent-setup": ["timing starts with the setup command after reserving the app, database and private route",
                               "isolated PostgreSQL and S3-compatible provider fixture"],
        "report-agent-recovery": ["merge failure is a task budget admission refusal",
                                  "isolated PostgreSQL and S3-compatible provider fixture"],
        "report-agent-background": ["real dispatcher sends signed HTTP to a private loopback receiver",
                                     "isolated PostgreSQL and S3-compatible provider fixture"],
    }
    result = {"schema": "gregale.run-" + cohort + "-functional/v1", "cohort": cohort,
            "functional_pass": True, "runtime_release_eligibility": False,
            "host": host, "tests": completed, "limitations": limitations[cohort]}
    if cohort == "report-agent-hosted":
        measurements = json.loads((root / "measurements.json").read_text())
        journeys = measurements.get("journeys", [])
        if (measurements.get("schema") != "gregale.report-agent-deployed/v1" or
                measurements.get("deployment_ms", 0) <= 0 or
                measurements.get("deploy_to_first_report_ms", 0) <= measurements["deployment_ms"] or
                [j.get("input") for j in journeys] != ["json", "csv"]):
            raise ValueError("incomplete deployed pilot measurements")
        for run, expected in zip(journeys, (3, 1)):
            if (run.get("physical_runs") != expected or run.get("apid_restarts") != 2 or
                    run.get("provider_writes") != 1 or run.get("retained_report_bytes", 0) <= 0 or
                    run.get("submit_to_verified_report_ms", 0) <= 0 or
                    run.get("run_compute_estimate_usd", 0) <= 0 or
                    run.get("agent_resident_reserved_mib_seconds", 0) <= 0):
                raise ValueError("incomplete Report Agent latency, cost or replay measurements")
        result["measurements"] = measurements
    if cohort == "report-agent-setup":
        measurements = json.loads((root / "measurements.json").read_text())
        identity = measurements.get("identity", {})
        if (measurements.get("schema") != "gregale.report-agent-setup/v1" or not measurements.get("reused_report") or
                not 0 < measurements.get("first_sample_verification_ms", 0) < measurements.get("first_setup_to_verified_report_ms", 0) or
                not 0 < measurements.get("replay_sample_verification_ms", 0) < measurements.get("replay_setup_to_verified_report_ms", 0) or
                any(measurements.get(key) != value for key, value in {
                    "deployment_count": 1, "request_count": 1, "physical_runs": 1, "provider_writes": 1, "provider_reads": 2,
                    "orders": 4, "units": 8, "revenue_cents": 6996,
                }.items()) or identity.get("report_bytes", 0) <= 0 or
                not re.fullmatch(r"[0-9a-f]{64}", identity.get("report_sha256", "")) or
                any(not identity.get(key) for key in ("deployment_id", "thread_id", "pipeline_id", "run_id", "delivery_id"))):
            raise ValueError("incomplete Report Agent setup and replay measurements")
        result["measurements"] = measurements
    if cohort == "report-agent-recovery":
        measurements = json.loads((root / "measurements.json").read_text())
        parent, child = measurements.get("parent_pipeline_id"), measurements.get("child_pipeline_id")
        if (measurements.get("schema") != "gregale.report-agent-recovery/v1" or not parent or not child or parent == child or
                not measurements.get("child_request_id") or
                not 0 < measurements.get("initial_cursor", 0) < measurements.get("resumed_terminal_cursor", 0) or
                any(measurements.get(key) is not True for key in (
                    "live_progress", "same_uuid_reused_child", "lost_ack_recovered", "original_receipt_unchanged",
                )) or any(measurements.get(key) != value for key, value in {
                    "merge_failure": "admission_refused", "parent_physical_runs": 2, "rerun_physical_runs": 1,
                    "physical_runs_after_replay": 3, "workflow_count": 2, "reused_steps": ["north", "south"], "revenue_cents": 6996,
                    "provider_writes": 1, "provider_reads": 2,
                }.items()) or measurements.get("report_bytes", 0) <= 0 or
                not re.fullmatch(r"[0-9a-f]{64}", measurements.get("report_sha256", ""))):
            raise ValueError("incomplete hosted Report Agent progress and recovery measurements")
        result["measurements"] = measurements
    if cohort == "report-agent-background":
        measurements = json.loads((root / "measurements.json").read_text())
        if (measurements.get("schema") != "gregale.report-agent-background/v1" or
                any(not measurements.get(key) for key in ("app_id", "pipeline_id", "request_id",
                                                          "webhook_delivery_id", "artifact_delivery_id")) or
                any(measurements.get(key) is not True for key in ("receiver_crashed_after_copy",
                                                                "fresh_receiver_recovered", "replay_verified")) or
                any(measurements.get(key) != value for key, value in {
                    "customer_poll_count": 0, "physical_runs": 3, "workflow_count": 1,
                    "provider_writes": 1, "provider_reads": 1, "revenue_cents": 6996,
                }.items()) or measurements.get("dispatcher_attempts", 0) < 2 or
                not 0 < measurements.get("completion_to_copy_ms", 0) < measurements.get("completion_to_download_ready_ms", 0) or
                measurements.get("report_bytes", 0) <= 0 or
                not re.fullmatch(r"[0-9a-f]{64}", measurements.get("report_sha256", ""))):
            raise ValueError("incomplete hosted Report Agent background delivery evidence")
        result["measurements"] = measurements
    return result


def task_verdict(host, root, exits, cohort="tasks"):
    if cohort not in ("tasks", "controls"): raise ValueError("unknown task cohort")
    validate_host(host)
    if any(exits.get(n)!=0 for n in (cohort,"leakcheck_before","leakcheck_after")):
        raise ValueError("task journey and leakchecks must exit zero")
    test_name="TestSourceRunControlsContinuationMetal" if cohort=="controls" else "TestSourceTaskContinuationMetal"
    tests=coverage(root/(cohort+".jsonl"),SOURCE,[test_name])
    return {"schema":"gregale.run-"+cohort+"-functional/v1","cohort":cohort,"functional_pass":True,
            "runtime_release_eligibility":False,"host":host,"tests":tests,
            "limitations":["node24 on the operator-selected Google KVM fixture; native production release eligibility is separate",
                           "continuation inbox and fresh apid readers; customer model invocation is outside this compute qualification"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--cohort", choices=("controls", "tasks", "baseline", "warm-oom", "template-clones", "native-recovery", "files-contracts", "extended-environments", "system-environments", "report-agent-json", "report-agent-files", "report-agent-hosted", "report-agent-setup", "report-agent-recovery", "report-agent-background"), default="baseline")
    args = parser.parse_args()
    root = args.directory
    try:
        host, exits = json.loads((root / "host.json").read_text()), json.loads((root / "process-exits.json").read_text())
        if args.cohort in ("tasks", "controls"):
            result = task_verdict(host,root,exits,args.cohort)
            log_names=(args.cohort+".jsonl",)
        elif args.cohort.startswith("report-agent-"):
            result = report_agent_verdict(host, root, exits, args.cohort)
            log_names = (args.cohort + ".jsonl",)
        elif args.cohort == "system-environments":
            result = system_environment_verdict(host, root, exits)
            log_names = ("system-environments.jsonl",)
        elif args.cohort == "extended-environments":
            result = extended_environment_verdict(host, root, exits)
            log_names = ("extended-environments.jsonl",)
        elif args.cohort == "files-contracts":
            result = files_contracts_verdict(host, root, exits)
            log_names = ("files-contracts.jsonl",)
        elif args.cohort == "native-recovery":
            result = native_recovery_verdict(host, root, exits)
            log_names = ("template-device-checks.jsonl", "template-clones.jsonl", "warm-oom-guest-checks.jsonl", "warm-oom-executor-checks.jsonl") + tuple(name + ".jsonl" for name in TEMPLATE_AUXILIARY)
        elif args.cohort == "template-clones":
            result = template_clone_verdict(host, root, exits)
            log_names = ("template-clones.jsonl", "warm-oom-guest-checks.jsonl", "warm-oom-executor-checks.jsonl") + tuple(name + ".jsonl" for name in TEMPLATE_AUXILIARY)
        elif args.cohort == "warm-oom":
            result = warm_oom_verdict(host, root, exits)
            log_names = ("warm-oom.jsonl", "warm-oom-guest-checks.jsonl", "warm-oom-executor-checks.jsonl")
        else:
            result = verdict(host, root / "source.jsonl", root / "oci.jsonl", exits)
            log_names = ("source.jsonl", "oci.jsonl")
        result["source"] = json.loads((root / "source.json").read_text())
        result["sha256"] = {name: hashlib.sha256((root / name).read_bytes()).hexdigest() for name in log_names + ("leakcheck-before.log", "leakcheck-after.log", "process-exits.json")}
    except (ValueError, OSError, KeyError) as error:
        raise SystemExit(f"Run lifecycle evidence refused: {error}") from error
    (root / "report.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
    print("Run lifecycle functional acceptance passed; qualification scope is recorded in report.json")


if __name__ == "__main__":
    main()
