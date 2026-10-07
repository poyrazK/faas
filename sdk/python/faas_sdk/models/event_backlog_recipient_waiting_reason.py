from typing import Literal

EventBacklogRecipientWaitingReason = Literal[
    "capacity_account",
    "capacity_app",
    "capacity_consumer",
    "ready",
    "receipt_processing",
    "retry_backoff",
    "routing_in_progress",
]

EVENT_BACKLOG_RECIPIENT_WAITING_REASON_VALUES: set[EventBacklogRecipientWaitingReason] = {
    "capacity_account",
    "capacity_app",
    "capacity_consumer",
    "ready",
    "receipt_processing",
    "retry_backoff",
    "routing_in_progress",
}


def check_event_backlog_recipient_waiting_reason(value: str) -> EventBacklogRecipientWaitingReason:
    if value in EVENT_BACKLOG_RECIPIENT_WAITING_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_BACKLOG_RECIPIENT_WAITING_REASON_VALUES!r}")
