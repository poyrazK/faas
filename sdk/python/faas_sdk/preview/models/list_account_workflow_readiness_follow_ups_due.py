from typing import Literal

ListAccountWorkflowReadinessFollowUpsDue = Literal["all", "overdue", "upcoming"]

LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_DUE_VALUES: set[ListAccountWorkflowReadinessFollowUpsDue] = {
    "all",
    "overdue",
    "upcoming",
}


def check_list_account_workflow_readiness_follow_ups_due(value: str) -> ListAccountWorkflowReadinessFollowUpsDue:
    if value in LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_DUE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_DUE_VALUES!r}"
    )
