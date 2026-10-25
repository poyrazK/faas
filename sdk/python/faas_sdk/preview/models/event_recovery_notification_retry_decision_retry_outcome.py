from typing import Literal

EventRecoveryNotificationRetryDecisionRetryOutcome = Literal[
    "failed", "not_applicable", "pending", "succeeded", "unknown"
]

EVENT_RECOVERY_NOTIFICATION_RETRY_DECISION_RETRY_OUTCOME_VALUES: set[
    EventRecoveryNotificationRetryDecisionRetryOutcome
] = {
    "failed",
    "not_applicable",
    "pending",
    "succeeded",
    "unknown",
}


def check_event_recovery_notification_retry_decision_retry_outcome(
    value: str,
) -> EventRecoveryNotificationRetryDecisionRetryOutcome:
    if value in EVENT_RECOVERY_NOTIFICATION_RETRY_DECISION_RETRY_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_RETRY_DECISION_RETRY_OUTCOME_VALUES!r}"
    )
