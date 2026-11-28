from typing import Literal

WorkflowDiagnosticStepStatus = Literal["awaiting_event", "dead", "failed", "pending", "running", "skipped", "succeeded"]

WORKFLOW_DIAGNOSTIC_STEP_STATUS_VALUES: set[WorkflowDiagnosticStepStatus] = {
    "awaiting_event",
    "dead",
    "failed",
    "pending",
    "running",
    "skipped",
    "succeeded",
}


def check_workflow_diagnostic_step_status(value: str) -> WorkflowDiagnosticStepStatus:
    if value in WORKFLOW_DIAGNOSTIC_STEP_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_DIAGNOSTIC_STEP_STATUS_VALUES!r}")
