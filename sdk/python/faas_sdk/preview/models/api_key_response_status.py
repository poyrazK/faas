from typing import Literal

APIKeyResponseStatus = Literal["active", "grace", "revoked"]

API_KEY_RESPONSE_STATUS_VALUES: set[APIKeyResponseStatus] = {
    "active",
    "grace",
    "revoked",
}


def check_api_key_response_status(value: str) -> APIKeyResponseStatus:
    if value in API_KEY_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_KEY_RESPONSE_STATUS_VALUES!r}")
