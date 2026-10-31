from typing import Literal

RouteMonitorIncidentRollbackStatus = Literal["claimed", "requested", "skipped"]

ROUTE_MONITOR_INCIDENT_ROLLBACK_STATUS_VALUES: set[RouteMonitorIncidentRollbackStatus] = {
    "claimed",
    "requested",
    "skipped",
}


def check_route_monitor_incident_rollback_status(value: str) -> RouteMonitorIncidentRollbackStatus:
    if value in ROUTE_MONITOR_INCIDENT_ROLLBACK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_INCIDENT_ROLLBACK_STATUS_VALUES!r}")
