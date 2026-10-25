from typing import Literal

ManagedRealtimePushDeliveryProvider = Literal["apns", "fcm", "webpush"]

MANAGED_REALTIME_PUSH_DELIVERY_PROVIDER_VALUES: set[ManagedRealtimePushDeliveryProvider] = {
    "apns",
    "fcm",
    "webpush",
}


def check_managed_realtime_push_delivery_provider(value: str) -> ManagedRealtimePushDeliveryProvider:
    if value in MANAGED_REALTIME_PUSH_DELIVERY_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_PUSH_DELIVERY_PROVIDER_VALUES!r}")
