from typing import Literal

TenantSurfaceResponseStatus = Literal["active", "deleted", "pending", "suspended"]

TENANT_SURFACE_RESPONSE_STATUS_VALUES: set[TenantSurfaceResponseStatus] = {
    "active",
    "deleted",
    "pending",
    "suspended",
}


def check_tenant_surface_response_status(value: str) -> TenantSurfaceResponseStatus:
    if value in TENANT_SURFACE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TENANT_SURFACE_RESPONSE_STATUS_VALUES!r}")
