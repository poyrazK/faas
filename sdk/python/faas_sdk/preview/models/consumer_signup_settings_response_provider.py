from typing import Literal

ConsumerSignupSettingsResponseProvider = Literal["stripe"]

CONSUMER_SIGNUP_SETTINGS_RESPONSE_PROVIDER_VALUES: set[ConsumerSignupSettingsResponseProvider] = {
    "stripe",
}


def check_consumer_signup_settings_response_provider(value: str) -> ConsumerSignupSettingsResponseProvider:
    if value in CONSUMER_SIGNUP_SETTINGS_RESPONSE_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CONSUMER_SIGNUP_SETTINGS_RESPONSE_PROVIDER_VALUES!r}"
    )
