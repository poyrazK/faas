from typing import Literal

EventRecoveryNotificationEvidenceSource = Literal[
    "capture_snapshot", "retained_deliveries", "retained_outbox", "unavailable"
]

EVENT_RECOVERY_NOTIFICATION_EVIDENCE_SOURCE_VALUES: set[EventRecoveryNotificationEvidenceSource] = {
    "capture_snapshot",
    "retained_deliveries",
    "retained_outbox",
    "unavailable",
}


def check_event_recovery_notification_evidence_source(value: str) -> EventRecoveryNotificationEvidenceSource:
    if value in EVENT_RECOVERY_NOTIFICATION_EVIDENCE_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_NOTIFICATION_EVIDENCE_SOURCE_VALUES!r}"
    )
