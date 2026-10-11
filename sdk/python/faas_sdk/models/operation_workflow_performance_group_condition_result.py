from typing import Literal

OperationWorkflowPerformanceGroupConditionResult = Literal["fail", "pass", "unknown"]

OPERATION_WORKFLOW_PERFORMANCE_GROUP_CONDITION_RESULT_VALUES: set[OperationWorkflowPerformanceGroupConditionResult] = {
    "fail",
    "pass",
    "unknown",
}


def check_operation_workflow_performance_group_condition_result(
    value: str,
) -> OperationWorkflowPerformanceGroupConditionResult:
    if value in OPERATION_WORKFLOW_PERFORMANCE_GROUP_CONDITION_RESULT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PERFORMANCE_GROUP_CONDITION_RESULT_VALUES!r}"
    )
