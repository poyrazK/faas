from typing import Literal

ConfigureManagedRealtimePushProviderResponse200Provider = Literal["apns", "fcm", "webpush"]

CONFIGURE_MANAGED_REALTIME_PUSH_PROVIDER_RESPONSE_200_PROVIDER_VALUES: set[
    ConfigureManagedRealtimePushProviderResponse200Provider
] = {
    "apns",
    "fcm",
    "webpush",
}


def check_configure_managed_realtime_push_provider_response_200_provider(
    value: str,
) -> ConfigureManagedRealtimePushProviderResponse200Provider:
    if value in CONFIGURE_MANAGED_REALTIME_PUSH_PROVIDER_RESPONSE_200_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CONFIGURE_MANAGED_REALTIME_PUSH_PROVIDER_RESPONSE_200_PROVIDER_VALUES!r}"
    )
