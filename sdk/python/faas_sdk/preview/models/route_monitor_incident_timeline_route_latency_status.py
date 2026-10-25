from typing import Literal

RouteMonitorIncidentTimelineRouteLatencyStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_INCIDENT_TIMELINE_ROUTE_LATENCY_STATUS_VALUES: set[RouteMonitorIncidentTimelineRouteLatencyStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_incident_timeline_route_latency_status(
    value: str,
) -> RouteMonitorIncidentTimelineRouteLatencyStatus:
    if value in ROUTE_MONITOR_INCIDENT_TIMELINE_ROUTE_LATENCY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_INCIDENT_TIMELINE_ROUTE_LATENCY_STATUS_VALUES!r}"
    )
