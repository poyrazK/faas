from typing import Literal

StoredWorkflowHealthRunStatus = Literal["awaiting_event", "dead", "failed", "pending", "running", "succeeded"]

STORED_WORKFLOW_HEALTH_RUN_STATUS_VALUES: set[StoredWorkflowHealthRunStatus] = {
    "awaiting_event",
    "dead",
    "failed",
    "pending",
    "running",
    "succeeded",
}


def check_stored_workflow_health_run_status(value: str) -> StoredWorkflowHealthRunStatus:
    if value in STORED_WORKFLOW_HEALTH_RUN_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STORED_WORKFLOW_HEALTH_RUN_STATUS_VALUES!r}")
