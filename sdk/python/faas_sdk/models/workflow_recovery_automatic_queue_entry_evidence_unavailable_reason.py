from typing import Literal

WorkflowRecoveryAutomaticQueueEntryEvidenceUnavailableReason = Literal["plan_changed", "review_not_retained"]

WORKFLOW_RECOVERY_AUTOMATIC_QUEUE_ENTRY_EVIDENCE_UNAVAILABLE_REASON_VALUES: set[
    WorkflowRecoveryAutomaticQueueEntryEvidenceUnavailableReason
] = {
    "plan_changed",
    "review_not_retained",
}


def check_workflow_recovery_automatic_queue_entry_evidence_unavailable_reason(
    value: str,
) -> WorkflowRecoveryAutomaticQueueEntryEvidenceUnavailableReason:
    if value in WORKFLOW_RECOVERY_AUTOMATIC_QUEUE_ENTRY_EVIDENCE_UNAVAILABLE_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_AUTOMATIC_QUEUE_ENTRY_EVIDENCE_UNAVAILABLE_REASON_VALUES!r}"
    )
