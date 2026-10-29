from typing import Literal

OperationWorkflowReadinessChangeStatus = Literal[
    "baseline_ahead",
    "changed",
    "contract_changed",
    "newly_blocked",
    "newly_ready",
    "state_unknown",
    "terminal",
    "transition_unavailable",
    "unchanged",
]

OPERATION_WORKFLOW_READINESS_CHANGE_STATUS_VALUES: set[OperationWorkflowReadinessChangeStatus] = {
    "baseline_ahead",
    "changed",
    "contract_changed",
    "newly_blocked",
    "newly_ready",
    "state_unknown",
    "terminal",
    "transition_unavailable",
    "unchanged",
}


def check_operation_workflow_readiness_change_status(value: str) -> OperationWorkflowReadinessChangeStatus:
    if value in OPERATION_WORKFLOW_READINESS_CHANGE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_CHANGE_STATUS_VALUES!r}"
    )
