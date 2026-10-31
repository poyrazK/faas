from typing import Literal

AdminStatusIncidentCreateRequestImpact = Literal["degraded", "major_outage", "partial_outage"]

ADMIN_STATUS_INCIDENT_CREATE_REQUEST_IMPACT_VALUES: set[AdminStatusIncidentCreateRequestImpact] = {
    "degraded",
    "major_outage",
    "partial_outage",
}


def check_admin_status_incident_create_request_impact(value: str) -> AdminStatusIncidentCreateRequestImpact:
    if value in ADMIN_STATUS_INCIDENT_CREATE_REQUEST_IMPACT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ADMIN_STATUS_INCIDENT_CREATE_REQUEST_IMPACT_VALUES!r}"
    )
