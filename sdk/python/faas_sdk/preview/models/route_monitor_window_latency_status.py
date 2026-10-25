from typing import Literal

RouteMonitorWindowLatencyStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_WINDOW_LATENCY_STATUS_VALUES: set[RouteMonitorWindowLatencyStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_window_latency_status(value: str) -> RouteMonitorWindowLatencyStatus:
    if value in ROUTE_MONITOR_WINDOW_LATENCY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_WINDOW_LATENCY_STATUS_VALUES!r}")
