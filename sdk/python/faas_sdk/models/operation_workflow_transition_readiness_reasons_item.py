from typing import Literal

OperationWorkflowTransitionReadinessReasonsItem = Literal[
    "application_blocked",
    "contract_version_mismatch",
    "dependency_required",
    "dependency_unmet",
    "effect_evidence_required",
    "from_state_mismatch",
    "invariant_evidence_required",
    "milestone_required",
    "policy_evidence_required",
    "revision_mismatch",
    "state_unknown",
    "terminal",
    "transition_undeclared",
]

OPERATION_WORKFLOW_TRANSITION_READINESS_REASONS_ITEM_VALUES: set[OperationWorkflowTransitionReadinessReasonsItem] = {
    "application_blocked",
    "contract_version_mismatch",
    "dependency_required",
    "dependency_unmet",
    "effect_evidence_required",
    "from_state_mismatch",
    "invariant_evidence_required",
    "milestone_required",
    "policy_evidence_required",
    "revision_mismatch",
    "state_unknown",
    "terminal",
    "transition_undeclared",
}


def check_operation_workflow_transition_readiness_reasons_item(
    value: str,
) -> OperationWorkflowTransitionReadinessReasonsItem:
    if value in OPERATION_WORKFLOW_TRANSITION_READINESS_REASONS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_TRANSITION_READINESS_REASONS_ITEM_VALUES!r}"
    )
