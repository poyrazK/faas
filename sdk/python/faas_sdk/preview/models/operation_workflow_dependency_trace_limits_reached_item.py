from typing import Literal

OperationWorkflowDependencyTraceLimitsReachedItem = Literal["dependencies", "depth", "findings", "workflows"]

OPERATION_WORKFLOW_DEPENDENCY_TRACE_LIMITS_REACHED_ITEM_VALUES: set[
    OperationWorkflowDependencyTraceLimitsReachedItem
] = {
    "dependencies",
    "depth",
    "findings",
    "workflows",
}


def check_operation_workflow_dependency_trace_limits_reached_item(
    value: str,
) -> OperationWorkflowDependencyTraceLimitsReachedItem:
    if value in OPERATION_WORKFLOW_DEPENDENCY_TRACE_LIMITS_REACHED_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_DEPENDENCY_TRACE_LIMITS_REACHED_ITEM_VALUES!r}"
    )
