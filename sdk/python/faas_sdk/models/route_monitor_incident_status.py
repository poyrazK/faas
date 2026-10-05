from typing import Literal

RouteMonitorIncidentStatus = Literal["open", "recovered", "superseded"]

ROUTE_MONITOR_INCIDENT_STATUS_VALUES: set[RouteMonitorIncidentStatus] = {
    "open",
    "recovered",
    "superseded",
}


def check_route_monitor_incident_status(value: str) -> RouteMonitorIncidentStatus:
    if value in ROUTE_MONITOR_INCIDENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_INCIDENT_STATUS_VALUES!r}")
