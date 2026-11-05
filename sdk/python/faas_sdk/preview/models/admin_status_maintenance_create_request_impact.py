from typing import Literal

AdminStatusMaintenanceCreateRequestImpact = Literal["maintenance"]

ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_IMPACT_VALUES: set[AdminStatusMaintenanceCreateRequestImpact] = {
    "maintenance",
}


def check_admin_status_maintenance_create_request_impact(value: str) -> AdminStatusMaintenanceCreateRequestImpact:
    if value in ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_IMPACT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ADMIN_STATUS_MAINTENANCE_CREATE_REQUEST_IMPACT_VALUES!r}"
    )
