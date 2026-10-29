from typing import Literal

DurableEntityHeadDeliveryStatus = Literal["dead", "failed", "in_flight", "pending", "succeeded", "unknown"]

DURABLE_ENTITY_HEAD_DELIVERY_STATUS_VALUES: set[DurableEntityHeadDeliveryStatus] = {
    "dead",
    "failed",
    "in_flight",
    "pending",
    "succeeded",
    "unknown",
}


def check_durable_entity_head_delivery_status(value: str) -> DurableEntityHeadDeliveryStatus:
    if value in DURABLE_ENTITY_HEAD_DELIVERY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DURABLE_ENTITY_HEAD_DELIVERY_STATUS_VALUES!r}")
