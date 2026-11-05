from typing import Literal

ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind = Literal[
    "coordinator_completion",
    "coordinator_evidence_review",
    "coordinator_revision",
    "decision_revalidation",
    "dependency_review",
    "escalation_acknowledgment",
    "follow_up",
    "fulfillment_review",
    "handoff_acceptance",
    "handoff_reassignment",
    "response_check_in",
    "scheduled_evidence_check",
]

LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_ACTION_KIND_VALUES: set[
    ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind
] = {
    "coordinator_completion",
    "coordinator_evidence_review",
    "coordinator_revision",
    "decision_revalidation",
    "dependency_review",
    "escalation_acknowledgment",
    "follow_up",
    "fulfillment_review",
    "handoff_acceptance",
    "handoff_reassignment",
    "response_check_in",
    "scheduled_evidence_check",
}


def check_list_platform_tenant_self_workflow_readiness_follow_ups_action_kind(
    value: str,
) -> ListPlatformTenantSelfWorkflowReadinessFollowUpsActionKind:
    if value in LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_ACTION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_PLATFORM_TENANT_SELF_WORKFLOW_READINESS_FOLLOW_UPS_ACTION_KIND_VALUES!r}"
    )
