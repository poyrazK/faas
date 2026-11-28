from typing import Literal

AppProtectionResponseState = Literal["none", "off", "on"]

APP_PROTECTION_RESPONSE_STATE_VALUES: set[AppProtectionResponseState] = {
    "none",
    "off",
    "on",
}


def check_app_protection_response_state(value: str) -> AppProtectionResponseState:
    if value in APP_PROTECTION_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_PROTECTION_RESPONSE_STATE_VALUES!r}")
