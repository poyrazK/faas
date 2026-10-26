from typing import Literal

ListAccountWorkflowReadinessFollowUpsActionTrigger = Literal["automatic", "human", "suggested"]

LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_ACTION_TRIGGER_VALUES: set[
    ListAccountWorkflowReadinessFollowUpsActionTrigger
] = {
    "automatic",
    "human",
    "suggested",
}


def check_list_account_workflow_readiness_follow_ups_action_trigger(
    value: str,
) -> ListAccountWorkflowReadinessFollowUpsActionTrigger:
    if value in LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_ACTION_TRIGGER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_ACTION_TRIGGER_VALUES!r}"
    )
