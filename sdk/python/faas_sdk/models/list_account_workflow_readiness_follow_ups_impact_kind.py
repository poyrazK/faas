from typing import Literal

ListAccountWorkflowReadinessFollowUpsImpactKind = Literal["milestone", "outcome"]

LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_KIND_VALUES: set[ListAccountWorkflowReadinessFollowUpsImpactKind] = {
    "milestone",
    "outcome",
}


def check_list_account_workflow_readiness_follow_ups_impact_kind(
    value: str,
) -> ListAccountWorkflowReadinessFollowUpsImpactKind:
    if value in LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_READINESS_FOLLOW_UPS_IMPACT_KIND_VALUES!r}"
    )
