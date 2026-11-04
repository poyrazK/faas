from typing import Literal

WorkflowStepResponseStatus = Literal["awaiting_event", "dead", "failed", "pending", "running", "skipped", "succeeded"]

WORKFLOW_STEP_RESPONSE_STATUS_VALUES: set[WorkflowStepResponseStatus] = {
    "awaiting_event",
    "dead",
    "failed",
    "pending",
    "running",
    "skipped",
    "succeeded",
}


def check_workflow_step_response_status(value: str) -> WorkflowStepResponseStatus:
    if value in WORKFLOW_STEP_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_STEP_RESPONSE_STATUS_VALUES!r}")
