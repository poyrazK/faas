#!/usr/bin/env bash
# A dedicated regional gate cannot qualify a skipped test or a missing drill.
NATIVE_REGIONAL_REQUIRED_TESTS=(
  TestNativeComputeRegionAcceptance
  TestNativeComputeRegionAcceptance/paused_admission_preserves_cold_wake
  TestNativeComputeRegionAcceptance/regional_saturation_refuses_remote_capacity
  TestNativeComputeRegionAcceptance/owner_recovery_uses_same_region_peer
  TestNativeComputeRegionAcceptance/whole_region_outage_remains_visible_and_refuses_remote_execution
  TestNativeRegionalBuildExportAcceptance
  TestNativeRegionalJobsAcceptance
  TestNativeRegionalJobsAcceptance/worker_loss_retries_on_same_region_peer
  TestNativeRegionalJobsAcceptance/whole_region_outage_queues_without_remote_boot_or_retry
)

native_regional_verdict() {
  local log="${1:?test log required}" required rc=0
  for required in "${NATIVE_REGIONAL_REQUIRED_TESTS[@]}"; do
    if ! grep -Eq "^[[:space:]]*--- PASS: ${required}[[:space:]]" "${log}"; then
      echo "regional acceptance: required drill did not pass: ${required}" >&2
      rc=1
    fi
  done
  if grep -Eq '^[[:space:]]*--- (SKIP|FAIL): TestNative(ComputeRegion|RegionalBuildExport|RegionalJobs)Acceptance([/[:space:]])' "${log}"; then
    echo 'regional acceptance: a required regional test was skipped or failed' >&2
    rc=1
  fi
  return "${rc}"
}
