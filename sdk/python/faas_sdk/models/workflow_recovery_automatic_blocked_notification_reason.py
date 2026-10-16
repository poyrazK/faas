from typing import Literal

WorkflowRecoveryAutomaticBlockedNotificationReason = Literal[
    "blocker_cleared", "evidence_unknown", "execution_quota", "job_paused", "terminal_outcome_not_saved"
]

WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_REASON_VALUES: set[
    WorkflowRecoveryAutomaticBlockedNotificationReason
] = {
    "blocker_cleared",
    "evidence_unknown",
    "execution_quota",
    "job_paused",
    "terminal_outcome_not_saved",
}


def check_workflow_recovery_automatic_blocked_notification_reason(
    value: str,
) -> WorkflowRecoveryAutomaticBlockedNotificationReason:
    if value in WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_RECOVERY_AUTOMATIC_BLOCKED_NOTIFICATION_REASON_VALUES!r}"
    )
