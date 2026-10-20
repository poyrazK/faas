from typing import Literal

EventRecipientBlockerResponseStatus = Literal["acceptance_replaced", "available", "not_waiting", "unavailable"]

EVENT_RECIPIENT_BLOCKER_RESPONSE_STATUS_VALUES: set[EventRecipientBlockerResponseStatus] = {
    "acceptance_replaced",
    "available",
    "not_waiting",
    "unavailable",
}


def check_event_recipient_blocker_response_status(value: str) -> EventRecipientBlockerResponseStatus:
    if value in EVENT_RECIPIENT_BLOCKER_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECIPIENT_BLOCKER_RESPONSE_STATUS_VALUES!r}")
