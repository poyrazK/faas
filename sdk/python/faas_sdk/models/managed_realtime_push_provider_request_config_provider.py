from typing import Literal

ManagedRealtimePushProviderRequestConfigProvider = Literal["apns", "fcm", "webpush"]

MANAGED_REALTIME_PUSH_PROVIDER_REQUEST_CONFIG_PROVIDER_VALUES: set[ManagedRealtimePushProviderRequestConfigProvider] = {
    "apns",
    "fcm",
    "webpush",
}


def check_managed_realtime_push_provider_request_config_provider(
    value: str,
) -> ManagedRealtimePushProviderRequestConfigProvider:
    if value in MANAGED_REALTIME_PUSH_PROVIDER_REQUEST_CONFIG_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_PUSH_PROVIDER_REQUEST_CONFIG_PROVIDER_VALUES!r}"
    )
