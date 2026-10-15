from typing import Literal

EventAcknowledgmentOutcomeKind = Literal["accepted", "completed", "expired", "renewal_rejected", "renewed"]

EVENT_ACKNOWLEDGMENT_OUTCOME_KIND_VALUES: set[EventAcknowledgmentOutcomeKind] = {
    "accepted",
    "completed",
    "expired",
    "renewal_rejected",
    "renewed",
}


def check_event_acknowledgment_outcome_kind(value: str) -> EventAcknowledgmentOutcomeKind:
    if value in EVENT_ACKNOWLEDGMENT_OUTCOME_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_ACKNOWLEDGMENT_OUTCOME_KIND_VALUES!r}")
