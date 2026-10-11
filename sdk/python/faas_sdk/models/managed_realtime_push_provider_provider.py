from typing import Literal

ManagedRealtimePushProviderProvider = Literal["apns", "fcm", "webpush"]

MANAGED_REALTIME_PUSH_PROVIDER_PROVIDER_VALUES: set[ManagedRealtimePushProviderProvider] = {
    "apns",
    "fcm",
    "webpush",
}


def check_managed_realtime_push_provider_provider(value: str) -> ManagedRealtimePushProviderProvider:
    if value in MANAGED_REALTIME_PUSH_PROVIDER_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_PUSH_PROVIDER_PROVIDER_VALUES!r}")
