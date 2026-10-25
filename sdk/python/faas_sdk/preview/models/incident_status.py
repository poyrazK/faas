from typing import Literal

IncidentStatus = Literal["open", "resolved"]

INCIDENT_STATUS_VALUES: set[IncidentStatus] = {
    "open",
    "resolved",
}


def check_incident_status(value: str) -> IncidentStatus:
    if value in INCIDENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INCIDENT_STATUS_VALUES!r}")
