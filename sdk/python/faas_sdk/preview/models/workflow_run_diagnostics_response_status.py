from typing import Literal

WorkflowRunDiagnosticsResponseStatus = Literal["awaiting_event", "dead", "failed", "pending", "running", "succeeded"]

WORKFLOW_RUN_DIAGNOSTICS_RESPONSE_STATUS_VALUES: set[WorkflowRunDiagnosticsResponseStatus] = {
    "awaiting_event",
    "dead",
    "failed",
    "pending",
    "running",
    "succeeded",
}


def check_workflow_run_diagnostics_response_status(value: str) -> WorkflowRunDiagnosticsResponseStatus:
    if value in WORKFLOW_RUN_DIAGNOSTICS_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_RUN_DIAGNOSTICS_RESPONSE_STATUS_VALUES!r}")
