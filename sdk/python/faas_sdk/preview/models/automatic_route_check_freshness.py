from typing import Literal

AutomaticRouteCheckFreshness = Literal["current", "stale", "unavailable"]

AUTOMATIC_ROUTE_CHECK_FRESHNESS_VALUES: set[AutomaticRouteCheckFreshness] = {
    "current",
    "stale",
    "unavailable",
}


def check_automatic_route_check_freshness(value: str) -> AutomaticRouteCheckFreshness:
    if value in AUTOMATIC_ROUTE_CHECK_FRESHNESS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATIC_ROUTE_CHECK_FRESHNESS_VALUES!r}")
