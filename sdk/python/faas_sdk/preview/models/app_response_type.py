from typing import Literal

AppResponseType = Literal["app", "function"]

APP_RESPONSE_TYPE_VALUES: set[AppResponseType] = {
    "app",
    "function",
}


def check_app_response_type(value: str) -> AppResponseType:
    if value in APP_RESPONSE_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_TYPE_VALUES!r}")
