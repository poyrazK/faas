from typing import Literal

OperationWorkflowDependencyFindingLimit = Literal["dependencies", "depth", "findings", "workflows"]

OPERATION_WORKFLOW_DEPENDENCY_FINDING_LIMIT_VALUES: set[OperationWorkflowDependencyFindingLimit] = {
    "dependencies",
    "depth",
    "findings",
    "workflows",
}


def check_operation_workflow_dependency_finding_limit(value: str) -> OperationWorkflowDependencyFindingLimit:
    if value in OPERATION_WORKFLOW_DEPENDENCY_FINDING_LIMIT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_DEPENDENCY_FINDING_LIMIT_VALUES!r}"
    )
