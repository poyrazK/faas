from typing import Literal

HostingRouteCheckSetStatus = Literal["failed", "unavailable", "verified"]

HOSTING_ROUTE_CHECK_SET_STATUS_VALUES: set[HostingRouteCheckSetStatus] = {
    "failed",
    "unavailable",
    "verified",
}


def check_hosting_route_check_set_status(value: str) -> HostingRouteCheckSetStatus:
    if value in HOSTING_ROUTE_CHECK_SET_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTING_ROUTE_CHECK_SET_STATUS_VALUES!r}")
