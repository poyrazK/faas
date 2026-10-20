from typing import Literal

ListAccountWorkflowReadinessFollowUpsImpactPriority = Literal["critical", "high", "normal"]

LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_PRIORITY_VALUES: set[
    ListAccountWorkflowReadinessFollowUpsImpactPriority
] = {
    "critical",
    "high",
    "normal",
}


def check_list_account_workflow_readiness_follow_ups_impact_priority(
    value: str,
) -> ListAccountWorkflowReadinessFollowUpsImpactPriority:
    if value in LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_PRIORITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_PRIORITY_VALUES!r}"
    )
