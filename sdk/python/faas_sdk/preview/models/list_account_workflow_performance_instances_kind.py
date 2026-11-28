from typing import Literal

ListAccountWorkflowPerformanceInstancesKind = Literal["decision", "path"]

LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_KIND_VALUES: set[ListAccountWorkflowPerformanceInstancesKind] = {
    "decision",
    "path",
}


def check_list_account_workflow_performance_instances_kind(value: str) -> ListAccountWorkflowPerformanceInstancesKind:
    if value in LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_KIND_VALUES!r}"
    )
