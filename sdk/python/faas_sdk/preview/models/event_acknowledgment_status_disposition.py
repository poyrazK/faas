from typing import Literal

EventAcknowledgmentStatusDisposition = Literal["dead-letter", "retry"]

EVENT_ACKNOWLEDGMENT_STATUS_DISPOSITION_VALUES: set[EventAcknowledgmentStatusDisposition] = {
    "dead-letter",
    "retry",
}


def check_event_acknowledgment_status_disposition(value: str) -> EventAcknowledgmentStatusDisposition:
    if value in EVENT_ACKNOWLEDGMENT_STATUS_DISPOSITION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_ACKNOWLEDGMENT_STATUS_DISPOSITION_VALUES!r}")
