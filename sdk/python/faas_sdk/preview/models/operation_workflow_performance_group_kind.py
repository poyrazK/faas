from typing import Literal

OperationWorkflowPerformanceGroupKind = Literal["decision", "path"]

OPERATION_WORKFLOW_PERFORMANCE_GROUP_KIND_VALUES: set[OperationWorkflowPerformanceGroupKind] = {
    "decision",
    "path",
}


def check_operation_workflow_performance_group_kind(value: str) -> OperationWorkflowPerformanceGroupKind:
    if value in OPERATION_WORKFLOW_PERFORMANCE_GROUP_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PERFORMANCE_GROUP_KIND_VALUES!r}")
