from typing import Literal

RouteMonitorIncidentTimelineEntryStatus = Literal["disabled", "healthy", "unknown", "violated"]

ROUTE_MONITOR_INCIDENT_TIMELINE_ENTRY_STATUS_VALUES: set[RouteMonitorIncidentTimelineEntryStatus] = {
    "disabled",
    "healthy",
    "unknown",
    "violated",
}


def check_route_monitor_incident_timeline_entry_status(value: str) -> RouteMonitorIncidentTimelineEntryStatus:
    if value in ROUTE_MONITOR_INCIDENT_TIMELINE_ENTRY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_INCIDENT_TIMELINE_ENTRY_STATUS_VALUES!r}"
    )
