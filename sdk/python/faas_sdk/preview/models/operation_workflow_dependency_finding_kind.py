from typing import Literal

OperationWorkflowDependencyFindingKind = Literal[
    "awaiting_application",
    "cycle",
    "deadline_overdue",
    "outcome_mismatch",
    "outcome_unknown",
    "reported_blockers",
    "state_stale",
    "state_unknown",
    "trace_limit",
]

OPERATION_WORKFLOW_DEPENDENCY_FINDING_KIND_VALUES: set[OperationWorkflowDependencyFindingKind] = {
    "awaiting_application",
    "cycle",
    "deadline_overdue",
    "outcome_mismatch",
    "outcome_unknown",
    "reported_blockers",
    "state_stale",
    "state_unknown",
    "trace_limit",
}


def check_operation_workflow_dependency_finding_kind(value: str) -> OperationWorkflowDependencyFindingKind:
    if value in OPERATION_WORKFLOW_DEPENDENCY_FINDING_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_DEPENDENCY_FINDING_KIND_VALUES!r}"
    )
