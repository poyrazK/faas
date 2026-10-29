from typing import Literal

OperationWorkflowConditionOutcomeGroupConditionResult = Literal["fail", "pass", "unknown"]

OPERATION_WORKFLOW_CONDITION_OUTCOME_GROUP_CONDITION_RESULT_VALUES: set[
    OperationWorkflowConditionOutcomeGroupConditionResult
] = {
    "fail",
    "pass",
    "unknown",
}


def check_operation_workflow_condition_outcome_group_condition_result(
    value: str,
) -> OperationWorkflowConditionOutcomeGroupConditionResult:
    if value in OPERATION_WORKFLOW_CONDITION_OUTCOME_GROUP_CONDITION_RESULT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_CONDITION_OUTCOME_GROUP_CONDITION_RESULT_VALUES!r}"
    )
