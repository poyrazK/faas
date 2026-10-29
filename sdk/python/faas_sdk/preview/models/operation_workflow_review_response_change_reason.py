from typing import Literal

OperationWorkflowReviewResponseChangeReason = Literal[
    "check_in_overdue",
    "decision_review_overdue",
    "escalation_acknowledged",
    "escalation_action_reviewed",
    "escalation_commitment_completed",
    "escalation_commitment_revised",
    "handoff_accepted",
    "handoff_declined",
    "handoff_reassigned",
    "human_update",
    "scheduled_review_canceled",
    "scheduled_review_rescheduled",
]

OPERATION_WORKFLOW_REVIEW_RESPONSE_CHANGE_REASON_VALUES: set[OperationWorkflowReviewResponseChangeReason] = {
    "check_in_overdue",
    "decision_review_overdue",
    "escalation_acknowledged",
    "escalation_action_reviewed",
    "escalation_commitment_completed",
    "escalation_commitment_revised",
    "handoff_accepted",
    "handoff_declined",
    "handoff_reassigned",
    "human_update",
    "scheduled_review_canceled",
    "scheduled_review_rescheduled",
}


def check_operation_workflow_review_response_change_reason(value: str) -> OperationWorkflowReviewResponseChangeReason:
    if value in OPERATION_WORKFLOW_REVIEW_RESPONSE_CHANGE_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVIEW_RESPONSE_CHANGE_REASON_VALUES!r}"
    )
