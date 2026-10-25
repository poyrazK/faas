from typing import Literal

IncidentSource = Literal["alert", "platform", "route_monitor"]

INCIDENT_SOURCE_VALUES: set[IncidentSource] = {
    "alert",
    "platform",
    "route_monitor",
}


def check_incident_source(value: str) -> IncidentSource:
    if value in INCIDENT_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INCIDENT_SOURCE_VALUES!r}")
