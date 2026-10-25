from typing import Literal

OperationWorkflowDependentInstanceDependencyStatus = Literal[
    "outcome_mismatch", "satisfied", "terminal", "unknown", "waiting"
]

OPERATION_WORKFLOW_DEPENDENT_INSTANCE_DEPENDENCY_STATUS_VALUES: set[
    OperationWorkflowDependentInstanceDependencyStatus
] = {
    "outcome_mismatch",
    "satisfied",
    "terminal",
    "unknown",
    "waiting",
}


def check_operation_workflow_dependent_instance_dependency_status(
    value: str,
) -> OperationWorkflowDependentInstanceDependencyStatus:
    if value in OPERATION_WORKFLOW_DEPENDENT_INSTANCE_DEPENDENCY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_DEPENDENT_INSTANCE_DEPENDENCY_STATUS_VALUES!r}"
    )
