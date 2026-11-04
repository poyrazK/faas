from typing import Literal

OperationWorkflowCaseNextActionKind = Literal[
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

OPERATION_WORKFLOW_CASE_NEXT_ACTION_KIND_VALUES: set[OperationWorkflowCaseNextActionKind] = {
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


def check_operation_workflow_case_next_action_kind(value: str) -> OperationWorkflowCaseNextActionKind:
    if value in OPERATION_WORKFLOW_CASE_NEXT_ACTION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_CASE_NEXT_ACTION_KIND_VALUES!r}")
