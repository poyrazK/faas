from typing import Literal

ApplyPlatformTenantSurfaceResponseStatus = Literal["active", "pending", "suspended"]

APPLY_PLATFORM_TENANT_SURFACE_RESPONSE_STATUS_VALUES: set[ApplyPlatformTenantSurfaceResponseStatus] = {
    "active",
    "pending",
    "suspended",
}


def check_apply_platform_tenant_surface_response_status(value: str) -> ApplyPlatformTenantSurfaceResponseStatus:
    if value in APPLY_PLATFORM_TENANT_SURFACE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLY_PLATFORM_TENANT_SURFACE_RESPONSE_STATUS_VALUES!r}"
    )
