from typing import Literal

ManagedRealtimePushDeliveryPriority = Literal["low", "normal", "urgent"]

MANAGED_REALTIME_PUSH_DELIVERY_PRIORITY_VALUES: set[ManagedRealtimePushDeliveryPriority] = {
    "low",
    "normal",
    "urgent",
}


def check_managed_realtime_push_delivery_priority(value: str) -> ManagedRealtimePushDeliveryPriority:
    if value in MANAGED_REALTIME_PUSH_DELIVERY_PRIORITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_PUSH_DELIVERY_PRIORITY_VALUES!r}")
