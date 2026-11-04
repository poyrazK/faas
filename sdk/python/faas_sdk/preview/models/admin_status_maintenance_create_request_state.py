from typing import Literal

AdminStatusMaintenanceCreateRequestState = Literal["scheduled"]

ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_STATE_VALUES: set[AdminStatusMaintenanceCreateRequestState] = {
    "scheduled",
}


def check_admin_status_maintenance_create_request_state(value: str) -> AdminStatusMaintenanceCreateRequestState:
    if value in ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_STATE_VALUES!r}"
    )
