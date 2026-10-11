from typing import Literal

OperationWorkflowDependencyTraceWorkflowLimit = Literal[64]

OPERATION_WORKFLOW_DEPENDENCY_TRACE_WORKFLOW_LIMIT_VALUES: set[OperationWorkflowDependencyTraceWorkflowLimit] = {
    64,
}


def check_operation_workflow_dependency_trace_workflow_limit(
    value: int,
) -> OperationWorkflowDependencyTraceWorkflowLimit:
    if value in OPERATION_WORKFLOW_DEPENDENCY_TRACE_WORKFLOW_LIMIT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_DEPENDENCY_TRACE_WORKFLOW_LIMIT_VALUES!r}"
    )
