from typing import Literal

GetEventBacklogWaitingReason = Literal[
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

GET_EVENT_BACKLOG_WAITING_REASON_VALUES: set[GetEventBacklogWaitingReason] = {
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


def check_get_event_backlog_waiting_reason(value: str) -> GetEventBacklogWaitingReason:
    if value in GET_EVENT_BACKLOG_WAITING_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_EVENT_BACKLOG_WAITING_REASON_VALUES!r}")
