from typing import Literal

ManagedRealtimePushDeliveryStatus = Literal["cancelled", "failed", "pending", "sending", "sent"]

MANAGED_REALTIME_PUSH_DELIVERY_STATUS_VALUES: set[ManagedRealtimePushDeliveryStatus] = {
    "cancelled",
    "failed",
    "pending",
    "sending",
    "sent",
}


def check_managed_realtime_push_delivery_status(value: str) -> ManagedRealtimePushDeliveryStatus:
    if value in MANAGED_REALTIME_PUSH_DELIVERY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_PUSH_DELIVERY_STATUS_VALUES!r}")
