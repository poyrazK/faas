from typing import Literal

OperationWorkflowActiveConditionGroupConditionResult = Literal["fail", "pass", "unknown"]

OPERATION_WORKFLOW_ACTIVE_CONDITION_GROUP_CONDITION_RESULT_VALUES: set[
    OperationWorkflowActiveConditionGroupConditionResult
] = {
    "fail",
    "pass",
    "unknown",
}


def check_operation_workflow_active_condition_group_condition_result(
    value: str,
) -> OperationWorkflowActiveConditionGroupConditionResult:
    if value in OPERATION_WORKFLOW_ACTIVE_CONDITION_GROUP_CONDITION_RESULT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_ACTIVE_CONDITION_GROUP_CONDITION_RESULT_VALUES!r}"
    )
