from typing import Literal

ListAccountWorkflowReadinessFollowUpsSort = Literal["business_impact", "due"]

LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_SORT_VALUES: set[ListAccountWorkflowReadinessFollowUpsSort] = {
    "business_impact",
    "due",
}


def check_list_account_workflow_readiness_follow_ups_sort(value: str) -> ListAccountWorkflowReadinessFollowUpsSort:
    if value in LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_SORT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_SORT_VALUES!r}"
    )
