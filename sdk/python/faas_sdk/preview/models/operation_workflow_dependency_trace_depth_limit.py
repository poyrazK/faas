from typing import Literal

OperationWorkflowDependencyTraceDepthLimit = Literal[8]

OPERATION_WORKFLOW_DEPENDENCY_TRACE_DEPTH_LIMIT_VALUES: set[OperationWorkflowDependencyTraceDepthLimit] = {
    8,
}


def check_operation_workflow_dependency_trace_depth_limit(value: int) -> OperationWorkflowDependencyTraceDepthLimit:
    if value in OPERATION_WORKFLOW_DEPENDENCY_TRACE_DEPTH_LIMIT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_DEPENDENCY_TRACE_DEPTH_LIMIT_VALUES!r}"
    )
