from typing import Literal

EventBacklogRecipientWaitingReason = Literal[
    "capacity_account",
    "capacity_app",
    "capacity_consumer",
    "circuit_open",
    "circuit_probe_wait",
    "circuit_recovery_rate_limited",
    "ordering_blocked",
    "ready",
    "receipt_processing",
    "retry_backoff",
    "routing_in_progress",
    "subscription_paused",
    "subscription_rate_limited",
    "workflow_routing",
]

EVENT_BACKLOG_RECIPIENT_WAITING_REASON_VALUES: set[EventBacklogRecipientWaitingReason] = {
    "capacity_account",
    "capacity_app",
    "capacity_consumer",
    "circuit_open",
    "circuit_probe_wait",
    "circuit_recovery_rate_limited",
    "ordering_blocked",
    "ready",
    "receipt_processing",
    "retry_backoff",
    "routing_in_progress",
    "subscription_paused",
    "subscription_rate_limited",
    "workflow_routing",
}


def check_event_backlog_recipient_waiting_reason(value: str) -> EventBacklogRecipientWaitingReason:
    if value in EVENT_BACKLOG_RECIPIENT_WAITING_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_BACKLOG_RECIPIENT_WAITING_REASON_VALUES!r}")
