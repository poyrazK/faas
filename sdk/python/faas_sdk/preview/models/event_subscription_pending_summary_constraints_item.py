from typing import Literal

EventSubscriptionPendingSummaryConstraintsItem = Literal[
    "active_leases",
    "capacity_account",
    "capacity_app",
    "capacity_consumer",
    "circuit_draining",
    "circuit_half_open",
    "circuit_open",
    "clock_uncertain",
    "legacy_rate_limit",
    "scheduled_retries",
    "subscription_paused",
]

EVENT_SUBSCRIPTION_PENDING_SUMMARY_CONSTRAINTS_ITEM_VALUES: set[EventSubscriptionPendingSummaryConstraintsItem] = {
    "active_leases",
    "capacity_account",
    "capacity_app",
    "capacity_consumer",
    "circuit_draining",
    "circuit_half_open",
    "circuit_open",
    "clock_uncertain",
    "legacy_rate_limit",
    "scheduled_retries",
    "subscription_paused",
}


def check_event_subscription_pending_summary_constraints_item(
    value: str,
) -> EventSubscriptionPendingSummaryConstraintsItem:
    if value in EVENT_SUBSCRIPTION_PENDING_SUMMARY_CONSTRAINTS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_PENDING_SUMMARY_CONSTRAINTS_ITEM_VALUES!r}"
    )
