from typing import Literal

PlatformTenantSelfActivationSurfaceResponseStatus = Literal["active", "pending", "suspended"]

PLATFORM_TENANT_SELF_ACTIVATION_SURFACE_RESPONSE_STATUS_VALUES: set[
    PlatformTenantSelfActivationSurfaceResponseStatus
] = {
    "active",
    "pending",
    "suspended",
}


def check_platform_tenant_self_activation_surface_response_status(
    value: str,
) -> PlatformTenantSelfActivationSurfaceResponseStatus:
    if value in PLATFORM_TENANT_SELF_ACTIVATION_SURFACE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_SELF_ACTIVATION_SURFACE_RESPONSE_STATUS_VALUES!r}"
    )
