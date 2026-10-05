from typing import Literal

RouteMonitorWindowErrorStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_WINDOW_ERROR_STATUS_VALUES: set[RouteMonitorWindowErrorStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_window_error_status(value: str) -> RouteMonitorWindowErrorStatus:
    if value in ROUTE_MONITOR_WINDOW_ERROR_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_WINDOW_ERROR_STATUS_VALUES!r}")
