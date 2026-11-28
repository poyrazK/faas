from typing import Literal

ConfigureManagedRealtimePushProviderProvider = Literal["apns", "fcm", "webpush"]

CONFIGURE_MANAGED_REALTIME_PUSH_PROVIDER_PROVIDER_VALUES: set[ConfigureManagedRealtimePushProviderProvider] = {
    "apns",
    "fcm",
    "webpush",
}


def check_configure_managed_realtime_push_provider_provider(value: str) -> ConfigureManagedRealtimePushProviderProvider:
    if value in CONFIGURE_MANAGED_REALTIME_PUSH_PROVIDER_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CONFIGURE_MANAGED_REALTIME_PUSH_PROVIDER_PROVIDER_VALUES!r}"
    )
