from typing import Literal

EventAcknowledgmentHistoryQueryKind = Literal[
    "accepted", "completed", "dead_letter_requested", "expired", "renewal_rejected", "renewed", "retry_requested"
]

EVENT_ACKNOWLEDGMENT_HISTORY_QUERY_KIND_VALUES: set[EventAcknowledgmentHistoryQueryKind] = {
    "accepted",
    "completed",
    "dead_letter_requested",
    "expired",
    "renewal_rejected",
    "renewed",
    "retry_requested",
}


def check_event_acknowledgment_history_query_kind(value: str) -> EventAcknowledgmentHistoryQueryKind:
    if value in EVENT_ACKNOWLEDGMENT_HISTORY_QUERY_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_ACKNOWLEDGMENT_HISTORY_QUERY_KIND_VALUES!r}")
