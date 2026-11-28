from typing import Literal

RouteMonitorFindingErrorStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_FINDING_ERROR_STATUS_VALUES: set[RouteMonitorFindingErrorStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_finding_error_status(value: str) -> RouteMonitorFindingErrorStatus:
    if value in ROUTE_MONITOR_FINDING_ERROR_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_FINDING_ERROR_STATUS_VALUES!r}")
