from typing import Literal

AdminStatusMaintenanceCreateRequestKind = Literal["maintenance"]

ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_KIND_VALUES: set[AdminStatusMaintenanceCreateRequestKind] = {
    "maintenance",
}


def check_admin_status_maintenance_create_request_kind(value: str) -> AdminStatusMaintenanceCreateRequestKind:
    if value in ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_KIND_VALUES!r}"
    )
