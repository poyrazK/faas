from typing import Literal

WorkflowRecoveryAutomaticBlockedNotificationEvidenceKind = Literal["child", "initial", "parent"]

WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_EVIDENCE_KIND_VALUES: set[
    WorkflowRecoveryAutomaticBlockedNotificationEvidenceKind
] = {
    "child",
    "initial",
    "parent",
}


def check_workflow_recovery_automatic_blocked_notification_evidence_kind(
    value: str,
) -> WorkflowRecoveryAutomaticBlockedNotificationEvidenceKind:
    if value in WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_EVIDENCE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_EVIDENCE_KIND_VALUES!r}"
    )
