from typing import Literal

AdminStatusIncidentCreateRequestState = Literal["identified", "investigating", "monitoring"]

ADMIN_STATUS_INCIDENT_CREATE_REQUEST_STATE_VALUES: set[AdminStatusIncidentCreateRequestState] = {
    "identified",
    "investigating",
    "monitoring",
}


def check_admin_status_incident_create_request_state(value: str) -> AdminStatusIncidentCreateRequestState:
    if value in ADMIN_STATUS_INCIDENT_CREATE_REQUEST_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ADMIN_STATUS_INCIDENT_CREATE_REQUEST_STATE_VALUES!r}"
    )
