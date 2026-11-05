from typing import Literal

OperationWorkflowReadinessChangeResolvedReasonsItem = Literal[
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

OPERATION_WORKFLOW_READINESS_CHANGE_RESOLVED_REASONS_ITEM_VALUES: set[
    OperationWorkflowReadinessChangeResolvedReasonsItem
] = {
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


def check_operation_workflow_readiness_change_resolved_reasons_item(
    value: str,
) -> OperationWorkflowReadinessChangeResolvedReasonsItem:
    if value in OPERATION_WORKFLOW_READINESS_CHANGE_RESOLVED_REASONS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_CHANGE_RESOLVED_REASONS_ITEM_VALUES!r}"
    )
