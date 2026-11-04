from typing import Literal

WorkflowRunDiagnosticsResponseStateReason = Literal[
    "app_capacity",
    "cancelled",
    "dead",
    "failed",
    "parked_wait",
    "ready",
    "retry_backoff",
    "running",
    "scheduled",
    "succeeded",
    "tenant_capacity",
    "workflow_capacity",
]

WORKFLOW_RUN_DIAGNOSTICS_RESPONSE_STATE_REASON_VALUES: set[WorkflowRunDiagnosticsResponseStateReason] = {
    "app_capacity",
    "cancelled",
    "dead",
    "failed",
    "parked_wait",
    "ready",
    "retry_backoff",
    "running",
    "scheduled",
    "succeeded",
    "tenant_capacity",
    "workflow_capacity",
}


def check_workflow_run_diagnostics_response_state_reason(value: str) -> WorkflowRunDiagnosticsResponseStateReason:
    if value in WORKFLOW_RUN_DIAGNOSTICS_RESPONSE_STATE_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RUN_DIAGNOSTICS_RESPONSE_STATE_REASON_VALUES!r}"
    )
