from typing import Literal

WorkflowQueuedRunCancelOutcomeStatus = Literal["awaiting_event", "dead", "failed", "pending", "running", "succeeded"]

WORKFLOW_QUEUED_RUN_CANCEL_OUTCOME_STATUS_VALUES: set[WorkflowQueuedRunCancelOutcomeStatus] = {
    "awaiting_event",
    "dead",
    "failed",
    "pending",
    "running",
    "succeeded",
}


def check_workflow_queued_run_cancel_outcome_status(value: str) -> WorkflowQueuedRunCancelOutcomeStatus:
    if value in WORKFLOW_QUEUED_RUN_CANCEL_OUTCOME_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_QUEUED_RUN_CANCEL_OUTCOME_STATUS_VALUES!r}")
