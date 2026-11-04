from typing import Literal

ManagedExecutionWorkflowResponseStatus = Literal["failed", "queued", "running", "succeeded"]

MANAGED_EXECUTION_WORKFLOW_RESPONSE_STATUS_VALUES: set[ManagedExecutionWorkflowResponseStatus] = {
    "failed",
    "queued",
    "running",
    "succeeded",
}


def check_managed_execution_workflow_response_status(value: str) -> ManagedExecutionWorkflowResponseStatus:
    if value in MANAGED_EXECUTION_WORKFLOW_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_EXECUTION_WORKFLOW_RESPONSE_STATUS_VALUES!r}"
    )
