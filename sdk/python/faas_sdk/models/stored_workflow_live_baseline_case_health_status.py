from typing import Literal

StoredWorkflowLiveBaselineCaseHealthStatus = Literal["failed", "not_started", "passed", "pending", "unavailable"]

STORED_WORKFLOW_LIVE_BASELINE_CASE_HEALTH_STATUS_VALUES: set[StoredWorkflowLiveBaselineCaseHealthStatus] = {
    "failed",
    "not_started",
    "passed",
    "pending",
    "unavailable",
}


def check_stored_workflow_live_baseline_case_health_status(value: str) -> StoredWorkflowLiveBaselineCaseHealthStatus:
    if value in STORED_WORKFLOW_LIVE_BASELINE_CASE_HEALTH_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_LIVE_BASELINE_CASE_HEALTH_STATUS_VALUES!r}"
    )
