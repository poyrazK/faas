from typing import Literal

ListAccountWorkflowPerformanceInstancesConditionResult = Literal["fail", "pass", "unknown"]

LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_RESULT_VALUES: set[
    ListAccountWorkflowPerformanceInstancesConditionResult
] = {
    "fail",
    "pass",
    "unknown",
}


def check_list_account_workflow_performance_instances_condition_result(
    value: str,
) -> ListAccountWorkflowPerformanceInstancesConditionResult:
    if value in LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_RESULT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_RESULT_VALUES!r}"
    )
