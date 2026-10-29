from typing import Literal

EventSubscriptionDeadlineRecipientWaitingReason = Literal[
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

EVENT_SUBSCRIPTION_DEADLINE_RECIPIENT_WAITING_REASON_VALUES: set[EventSubscriptionDeadlineRecipientWaitingReason] = {
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


def check_event_subscription_deadline_recipient_waiting_reason(
    value: str,
) -> EventSubscriptionDeadlineRecipientWaitingReason:
    if value in EVENT_SUBSCRIPTION_DEADLINE_RECIPIENT_WAITING_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_DEADLINE_RECIPIENT_WAITING_REASON_VALUES!r}"
    )
