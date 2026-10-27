from typing import Literal

ListEventSubscriptionDeadlinesWaitingReason = Literal[
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

LIST_EVENT_SUBSCRIPTION_DEADLINES_WAITING_REASON_VALUES: set[ListEventSubscriptionDeadlinesWaitingReason] = {
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


def check_list_event_subscription_deadlines_waiting_reason(value: str) -> ListEventSubscriptionDeadlinesWaitingReason:
    if value in LIST_EVENT_SUBSCRIPTION_DEADLINES_WAITING_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_EVENT_SUBSCRIPTION_DEADLINES_WAITING_REASON_VALUES!r}"
    )
