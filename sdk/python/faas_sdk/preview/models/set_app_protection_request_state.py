from typing import Literal

SetAppProtectionRequestState = Literal["off", "on"]

SET_APP_PROTECTION_REQUEST_STATE_VALUES: set[SetAppProtectionRequestState] = {
    "off",
    "on",
}


def check_set_app_protection_request_state(value: str) -> SetAppProtectionRequestState:
    if value in SET_APP_PROTECTION_REQUEST_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SET_APP_PROTECTION_REQUEST_STATE_VALUES!r}")
