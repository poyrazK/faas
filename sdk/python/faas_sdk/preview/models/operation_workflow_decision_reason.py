from typing import Literal

OperationWorkflowDecisionReason = Literal[
    "application_blocked",
    "deadline_overdue",
    "dependency_waiting",
    "no_declared_transition",
    "state_stale",
    "state_unknown",
    "terminal",
    "transitions_available",
]

OPERATION_WORKFLOW_DECISION_REASON_VALUES: set[OperationWorkflowDecisionReason] = {
    "application_blocked",
    "deadline_overdue",
    "dependency_waiting",
    "no_declared_transition",
    "state_stale",
    "state_unknown",
    "terminal",
    "transitions_available",
}


def check_operation_workflow_decision_reason(value: str) -> OperationWorkflowDecisionReason:
    if value in OPERATION_WORKFLOW_DECISION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_DECISION_REASON_VALUES!r}")
