from typing import Literal

ListAccountWorkflowReadinessQueueRequirement = Literal[
    "application_blocked",
    "contract_version_mismatch",
    "dependency_required",
    "dependency_unmet",
    "effect_evidence_required",
    "from_state_mismatch",
    "invariant_evidence_required",
    "milestone_required",
    "path_selection_invalid",
    "policy_evidence_required",
    "retained_milestone_expired",
    "retained_milestone_group_unmet",
    "retained_milestone_invalidated",
    "retained_milestone_required",
    "revision_mismatch",
    "state_unknown",
    "terminal",
    "transition_undeclared",
]

LIST_ACCOUNT_WORKFLOW_READINESS_QUEUE_REQUIREMENT_VALUES: set[ListAccountWorkflowReadinessQueueRequirement] = {
    "application_blocked",
    "contract_version_mismatch",
    "dependency_required",
    "dependency_unmet",
    "effect_evidence_required",
    "from_state_mismatch",
    "invariant_evidence_required",
    "milestone_required",
    "path_selection_invalid",
    "policy_evidence_required",
    "retained_milestone_expired",
    "retained_milestone_group_unmet",
    "retained_milestone_invalidated",
    "retained_milestone_required",
    "revision_mismatch",
    "state_unknown",
    "terminal",
    "transition_undeclared",
}


def check_list_account_workflow_readiness_queue_requirement(value: str) -> ListAccountWorkflowReadinessQueueRequirement:
    if value in LIST_ACCOUNT_WORKFLOW_READINESS_QUEUE_REQUIREMENT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_READINESS_QUEUE_REQUIREMENT_VALUES!r}"
    )
