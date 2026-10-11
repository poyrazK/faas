from typing import Literal

EventRecoveryNotificationRetryCandidateKind = Literal["admission", "execution"]

EVENT_RECOVERY_NOTIFICATION_RETRY_CANDIDATE_KIND_VALUES: set[EventRecoveryNotificationRetryCandidateKind] = {
    "admission",
    "execution",
}


def check_event_recovery_notification_retry_candidate_kind(value: str) -> EventRecoveryNotificationRetryCandidateKind:
    if value in EVENT_RECOVERY_NOTIFICATION_RETRY_CANDIDATE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_RETRY_CANDIDATE_KIND_VALUES!r}"
    )
