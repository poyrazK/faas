from typing import Literal

ListEventAcknowledgmentHistoryPageKind = Literal[
    "accepted", "completed", "dead_letter_requested", "expired", "renewal_rejected", "renewed", "retry_requested"
]

LIST_EVENT_ACKNOWLEDGMENT_HISTORY_PAGE_KIND_VALUES: set[ListEventAcknowledgmentHistoryPageKind] = {
    "accepted",
    "completed",
    "dead_letter_requested",
    "expired",
    "renewal_rejected",
    "renewed",
    "retry_requested",
}


def check_list_event_acknowledgment_history_page_kind(value: str) -> ListEventAcknowledgmentHistoryPageKind:
    if value in LIST_EVENT_ACKNOWLEDGMENT_HISTORY_PAGE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_EVENT_ACKNOWLEDGMENT_HISTORY_PAGE_KIND_VALUES!r}"
    )
