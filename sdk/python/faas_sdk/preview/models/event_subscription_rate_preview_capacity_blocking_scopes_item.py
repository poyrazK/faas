from typing import Literal

EventSubscriptionRatePreviewCapacityBlockingScopesItem = Literal["account", "app", "consumer"]

EVENT_SUBSCRIPTION_RATE_PREVIEW_CAPACITY_BLOCKING_SCOPES_ITEM_VALUES: set[
    EventSubscriptionRatePreviewCapacityBlockingScopesItem
] = {
    "account",
    "app",
    "consumer",
}


def check_event_subscription_rate_preview_capacity_blocking_scopes_item(
    value: str,
) -> EventSubscriptionRatePreviewCapacityBlockingScopesItem:
    if value in EVENT_SUBSCRIPTION_RATE_PREVIEW_CAPACITY_BLOCKING_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SUBSCRIPTION_RATE_PREVIEW_CAPACITY_BLOCKING_SCOPES_ITEM_VALUES!r}"
    )
