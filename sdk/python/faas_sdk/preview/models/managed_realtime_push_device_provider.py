from typing import Literal

ManagedRealtimePushDeviceProvider = Literal["apns", "fcm", "webpush"]

MANAGED_REALTIME_PUSH_DEVICE_PROVIDER_VALUES: set[ManagedRealtimePushDeviceProvider] = {
    "apns",
    "fcm",
    "webpush",
}


def check_managed_realtime_push_device_provider(value: str) -> ManagedRealtimePushDeviceProvider:
    if value in MANAGED_REALTIME_PUSH_DEVICE_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_PUSH_DEVICE_PROVIDER_VALUES!r}")
