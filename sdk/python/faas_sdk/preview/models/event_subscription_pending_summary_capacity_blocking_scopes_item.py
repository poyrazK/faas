from typing import Literal

EventSubscriptionPendingSummaryCapacityBlockingScopesItem = Literal["account", "app", "consumer"]

EVENT_SUBSCRIPTION_PENDING_SUMMARY_CAPACITY_BLOCKING_SCOPES_ITEM_VALUES: set[
    EventSubscriptionPendingSummaryCapacityBlockingScopesItem
] = {
    "account",
    "app",
    "consumer",
}


def check_event_subscription_pending_summary_capacity_blocking_scopes_item(
    value: str,
) -> EventSubscriptionPendingSummaryCapacityBlockingScopesItem:
    if value in EVENT_SUBSCRIPTION_PENDING_SUMMARY_CAPACITY_BLOCKING_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_PENDING_SUMMARY_CAPACITY_BLOCKING_SCOPES_ITEM_VALUES!r}"
    )
