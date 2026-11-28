from typing import Literal

RouteRemovalCheckStatus = Literal["blocked", "not_configured", "not_required", "passed"]

ROUTE_REMOVAL_CHECK_STATUS_VALUES: set[RouteRemovalCheckStatus] = {
    "blocked",
    "not_configured",
    "not_required",
    "passed",
}


def check_route_removal_check_status(value: str) -> RouteRemovalCheckStatus:
    if value in ROUTE_REMOVAL_CHECK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_REMOVAL_CHECK_STATUS_VALUES!r}")
