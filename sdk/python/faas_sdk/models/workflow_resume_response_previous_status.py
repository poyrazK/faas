from typing import Literal

WorkflowResumeResponsePreviousStatus = Literal["dead", "failed"]

WORKFLOW_RESUME_RESPONSE_PREVIOUS_STATUS_VALUES: set[WorkflowResumeResponsePreviousStatus] = {
    "dead",
    "failed",
}


def check_workflow_resume_response_previous_status(value: str) -> WorkflowResumeResponsePreviousStatus:
    if value in WORKFLOW_RESUME_RESPONSE_PREVIOUS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RESUME_RESPONSE_PREVIOUS_STATUS_VALUES!r}")
