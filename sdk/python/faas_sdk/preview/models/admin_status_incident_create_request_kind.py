from typing import Literal

AdminStatusIncidentCreateRequestKind = Literal["incident"]

ADMIN_STATUS_INCIDENT_CREATE_REQUEST_KIND_VALUES: set[AdminStatusIncidentCreateRequestKind] = {
    "incident",
}


def check_admin_status_incident_create_request_kind(value: str) -> AdminStatusIncidentCreateRequestKind:
    if value in ADMIN_STATUS_INCIDENT_CREATE_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ADMIN_STATUS_INCIDENT_CREATE_REQUEST_KIND_VALUES!r}")
