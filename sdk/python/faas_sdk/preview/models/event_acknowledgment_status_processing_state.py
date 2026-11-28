from typing import Literal

EventAcknowledgmentStatusProcessingState = Literal[
    "cancelled", "completed", "dead_letter", "delivering", "expired", "failed", "processing", "superseded", "waiting"
]

EVENT_ACKNOWLEDGMENT_STATUS_PROCESSING_STATE_VALUES: set[EventAcknowledgmentStatusProcessingState] = {
    "cancelled",
    "completed",
    "dead_letter",
    "delivering",
    "expired",
    "failed",
    "processing",
    "superseded",
    "waiting",
}


def check_event_acknowledgment_status_processing_state(value: str) -> EventAcknowledgmentStatusProcessingState:
    if value in EVENT_ACKNOWLEDGMENT_STATUS_PROCESSING_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_ACKNOWLEDGMENT_STATUS_PROCESSING_STATE_VALUES!r}"
    )
