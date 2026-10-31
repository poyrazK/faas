from typing import Literal

AutomaticRouteCheckLastErrorCode = Literal["check_failed"]

AUTOMATIC_ROUTE_CHECK_LAST_ERROR_CODE_VALUES: set[AutomaticRouteCheckLastErrorCode] = {
    "check_failed",
}


def check_automatic_route_check_last_error_code(value: str) -> AutomaticRouteCheckLastErrorCode:
    if value in AUTOMATIC_ROUTE_CHECK_LAST_ERROR_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATIC_ROUTE_CHECK_LAST_ERROR_CODE_VALUES!r}")
