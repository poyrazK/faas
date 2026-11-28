from typing import Literal

OperationWorkflowObligationContextStatus = Literal[
    "evidence_unavailable",
    "no_declared_transition",
    "partial_readiness",
    "terminal_reported",
    "transition_requirements_satisfied",
    "waiting_on_requirements",
]

OPERATION_WORKFLOW_OBLIGATION_CONTEXT_STATUS_VALUES: set[OperationWorkflowObligationContextStatus] = {
    "evidence_unavailable",
    "no_declared_transition",
    "partial_readiness",
    "terminal_reported",
    "transition_requirements_satisfied",
    "waiting_on_requirements",
}


def check_operation_workflow_obligation_context_status(value: str) -> OperationWorkflowObligationContextStatus:
    if value in OPERATION_WORKFLOW_OBLIGATION_CONTEXT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_OBLIGATION_CONTEXT_STATUS_VALUES!r}"
    )
