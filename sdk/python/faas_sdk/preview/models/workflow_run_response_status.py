from typing import Literal

WorkflowRunResponseStatus = Literal["awaiting_event", "dead", "failed", "pending", "running", "succeeded"]

WORKFLOW_RUN_RESPONSE_STATUS_VALUES: set[WorkflowRunResponseStatus] = {
    "awaiting_event",
    "dead",
    "failed",
    "pending",
    "running",
    "succeeded",
}


def check_workflow_run_response_status(value: str) -> WorkflowRunResponseStatus:
    if value in WORKFLOW_RUN_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RUN_RESPONSE_STATUS_VALUES!r}")
