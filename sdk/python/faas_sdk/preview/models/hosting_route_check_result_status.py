from typing import Literal

HostingRouteCheckResultStatus = Literal["failed", "skipped", "verified"]

HOSTING_ROUTE_CHECK_RESULT_STATUS_VALUES: set[HostingRouteCheckResultStatus] = {
    "failed",
    "skipped",
    "verified",
}


def check_hosting_route_check_result_status(value: str) -> HostingRouteCheckResultStatus:
    if value in HOSTING_ROUTE_CHECK_RESULT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTING_ROUTE_CHECK_RESULT_STATUS_VALUES!r}")
