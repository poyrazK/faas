from typing import Literal

RouteMonitorFindingLatencyStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_FINDING_LATENCY_STATUS_VALUES: set[RouteMonitorFindingLatencyStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_finding_latency_status(value: str) -> RouteMonitorFindingLatencyStatus:
    if value in ROUTE_MONITOR_FINDING_LATENCY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_FINDING_LATENCY_STATUS_VALUES!r}")
