from typing import Literal

OperationWorkflowDependencyTraceDependencyLimit = Literal[256]

OPERATION_WORKFLOW_DEPENDENCY_TRACE_DEPENDENCY_LIMIT_VALUES: set[OperationWorkflowDependencyTraceDependencyLimit] = {
    256,
}


def check_operation_workflow_dependency_trace_dependency_limit(
    value: int,
) -> OperationWorkflowDependencyTraceDependencyLimit:
    if value in OPERATION_WORKFLOW_DEPENDENCY_TRACE_DEPENDENCY_LIMIT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_DEPENDENCY_TRACE_DEPENDENCY_LIMIT_VALUES!r}"
    )
