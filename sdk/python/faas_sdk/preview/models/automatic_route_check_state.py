from typing import Literal

AutomaticRouteCheckState = Literal["complete", "pending", "retrying", "running"]

AUTOMATIC_ROUTE_CHECK_STATE_VALUES: set[AutomaticRouteCheckState] = {
    "complete",
    "pending",
    "retrying",
    "running",
}


def check_automatic_route_check_state(value: str) -> AutomaticRouteCheckState:
    if value in AUTOMATIC_ROUTE_CHECK_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATIC_ROUTE_CHECK_STATE_VALUES!r}")
