from typing import Literal

WorkflowStepAttemptResponseStatus = Literal["failed", "retrying", "running", "succeeded"]

WORKFLOW_STEP_ATTEMPT_RESPONSE_STATUS_VALUES: set[WorkflowStepAttemptResponseStatus] = {
    "failed",
    "retrying",
    "running",
    "succeeded",
}


def check_workflow_step_attempt_response_status(value: str) -> WorkflowStepAttemptResponseStatus:
    if value in WORKFLOW_STEP_ATTEMPT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_STEP_ATTEMPT_RESPONSE_STATUS_VALUES!r}")
