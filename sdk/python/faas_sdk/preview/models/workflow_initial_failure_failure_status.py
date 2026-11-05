from typing import Literal

WorkflowInitialFailureFailureStatus = Literal["dead", "failed"]

WORKFLOW_INITIAL_FAILURE_FAILURE_STATUS_VALUES: set[WorkflowInitialFailureFailureStatus] = {
    "dead",
    "failed",
}


def check_workflow_initial_failure_failure_status(value: str) -> WorkflowInitialFailureFailureStatus:
    if value in WORKFLOW_INITIAL_FAILURE_FAILURE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_INITIAL_FAILURE_FAILURE_STATUS_VALUES!r}")
