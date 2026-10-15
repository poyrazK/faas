from typing import Literal

EventAcknowledgmentHistoryQueryKind = Literal["accepted", "completed", "expired", "renewal_rejected", "renewed"]

EVENT_ACKNOWLEDGMENT_HISTORY_QUERY_KIND_VALUES: set[EventAcknowledgmentHistoryQueryKind] = {
    "accepted",
    "completed",
    "expired",
    "renewal_rejected",
    "renewed",
}


def check_event_acknowledgment_history_query_kind(value: str) -> EventAcknowledgmentHistoryQueryKind:
    if value in EVENT_ACKNOWLEDGMENT_HISTORY_QUERY_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_ACKNOWLEDGMENT_HISTORY_QUERY_KIND_VALUES!r}")
