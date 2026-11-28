from typing import Literal

EventAcknowledgmentOutcomeKind = Literal[
    "accepted", "completed", "dead_letter_requested", "expired", "renewal_rejected", "renewed", "retry_requested"
]

EVENT_ACKNOWLEDGMENT_OUTCOME_KIND_VALUES: set[EventAcknowledgmentOutcomeKind] = {
    "accepted",
    "completed",
    "dead_letter_requested",
    "expired",
    "renewal_rejected",
    "renewed",
    "retry_requested",
}


def check_event_acknowledgment_outcome_kind(value: str) -> EventAcknowledgmentOutcomeKind:
    if value in EVENT_ACKNOWLEDGMENT_OUTCOME_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_ACKNOWLEDGMENT_OUTCOME_KIND_VALUES!r}")
