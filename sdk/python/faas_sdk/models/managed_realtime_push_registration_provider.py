from typing import Literal

ManagedRealtimePushRegistrationProvider = Literal["apns", "fcm", "webpush"]

MANAGED_REALTIME_PUSH_REGISTRATION_PROVIDER_VALUES: set[ManagedRealtimePushRegistrationProvider] = {
    "apns",
    "fcm",
    "webpush",
}


def check_managed_realtime_push_registration_provider(value: str) -> ManagedRealtimePushRegistrationProvider:
    if value in MANAGED_REALTIME_PUSH_REGISTRATION_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_PUSH_REGISTRATION_PROVIDER_VALUES!r}"
    )
