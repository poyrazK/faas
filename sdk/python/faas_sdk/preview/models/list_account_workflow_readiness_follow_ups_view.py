from typing import Literal

ListAccountWorkflowReadinessFollowUpsView = Literal["follow_ups", "next_actions"]

LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_VIEW_VALUES: set[ListAccountWorkflowReadinessFollowUpsView] = {
    "follow_ups",
    "next_actions",
}


def check_list_account_workflow_readiness_follow_ups_view(value: str) -> ListAccountWorkflowReadinessFollowUpsView:
    if value in LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_VIEW_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_VIEW_VALUES!r}"
    )
