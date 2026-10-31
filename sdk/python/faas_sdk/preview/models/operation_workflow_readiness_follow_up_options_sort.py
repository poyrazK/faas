from typing import Literal

OperationWorkflowReadinessFollowUpOptionsSort = Literal["business_impact", "due"]

OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_SORT_VALUES: set[OperationWorkflowReadinessFollowUpOptionsSort] = {
    "business_impact",
    "due",
}


def check_operation_workflow_readiness_follow_up_options_sort(
    value: str,
) -> OperationWorkflowReadinessFollowUpOptionsSort:
    if value in OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_SORT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_FOLLOW_UP_OPTIONS_SORT_VALUES!r}"
    )
