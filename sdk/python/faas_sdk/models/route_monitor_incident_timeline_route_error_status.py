from typing import Literal

RouteMonitorIncidentTimelineRouteErrorStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_INCIDENT_TIMELINE_ROUTE_ERROR_STATUS_VALUES: set[RouteMonitorIncidentTimelineRouteErrorStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_incident_timeline_route_error_status(
    value: str,
) -> RouteMonitorIncidentTimelineRouteErrorStatus:
    if value in ROUTE_MONITOR_INCIDENT_TIMELINE_ROUTE_ERROR_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_INCIDENT_TIMELINE_ROUTE_ERROR_STATUS_VALUES!r}"
    )
