from typing import Literal

StoredWorkflowLiveBaselineHealthRenewalStatus = Literal["failed", "inconclusive", "passed"]

STORED_WORKFLOW_LIVE_BASELINE_HEALTH_RENEWAL_STATUS_VALUES: set[StoredWorkflowLiveBaselineHealthRenewalStatus] = {
    "failed",
    "inconclusive",
    "passed",
}


def check_stored_workflow_live_baseline_health_renewal_status(
    value: str,
) -> StoredWorkflowLiveBaselineHealthRenewalStatus:
    if value in STORED_WORKFLOW_LIVE_BASELINE_HEALTH_RENEWAL_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_LIVE_BASELINE_HEALTH_RENEWAL_STATUS_VALUES!r}"
    )
