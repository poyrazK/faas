from typing import Literal

APISandboxApprovalCheckStaleReason = Literal[
    "baseline_scenario_changed", "candidate_artifact_changed", "live_baseline_changed"
]

API_SANDBOX_APPROVAL_CHECK_STALE_REASON_VALUES: set[APISandboxApprovalCheckStaleReason] = {
    "baseline_scenario_changed",
    "candidate_artifact_changed",
    "live_baseline_changed",
}


def check_api_sandbox_approval_check_stale_reason(value: str) -> APISandboxApprovalCheckStaleReason:
    if value in API_SANDBOX_APPROVAL_CHECK_STALE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_APPROVAL_CHECK_STALE_REASON_VALUES!r}")
