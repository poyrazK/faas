from typing import Literal

RouteMonitorFindingStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_FINDING_STATUS_VALUES: set[RouteMonitorFindingStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_finding_status(value: str) -> RouteMonitorFindingStatus:
    if value in ROUTE_MONITOR_FINDING_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_FINDING_STATUS_VALUES!r}")
