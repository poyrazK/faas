from typing import Literal

WorkflowRecoveryQueueBlockedWebhookPayloadEvidenceKind = Literal["child", "initial", "parent"]

WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_EVIDENCE_KIND_VALUES: set[
    WorkflowRecoveryQueueBlockedWebhookPayloadEvidenceKind
] = {
    "child",
    "initial",
    "parent",
}


def check_workflow_recovery_queue_blocked_webhook_payload_evidence_kind(
    value: str,
) -> WorkflowRecoveryQueueBlockedWebhookPayloadEvidenceKind:
    if value in WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_EVIDENCE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_QUEUE_BLOCKED_WEBHOOK_PAYLOAD_EVIDENCE_KIND_VALUES!r}"
    )
