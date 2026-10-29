from typing import Literal

AppRoutesResponseSource = Literal["live", "partial", "unavailable"]

APP_ROUTES_RESPONSE_SOURCE_VALUES: set[AppRoutesResponseSource] = {
    "live",
    "partial",
    "unavailable",
}


def check_app_routes_response_source(value: str) -> AppRoutesResponseSource:
    if value in APP_ROUTES_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_ROUTES_RESPONSE_SOURCE_VALUES!r}")
