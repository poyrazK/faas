from typing import Literal

StatusIncidentSeverity = Literal["degraded", "full_outage", "maintenance", "partial_outage"]

STATUS_INCIDENT_SEVERITY_VALUES: set[StatusIncidentSeverity] = {
    "degraded",
    "full_outage",
    "maintenance",
    "partial_outage",
}


def check_status_incident_severity(value: str) -> StatusIncidentSeverity:
    if value in STATUS_INCIDENT_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STATUS_INCIDENT_SEVERITY_VALUES!r}")
