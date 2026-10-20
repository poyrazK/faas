from typing import Literal

ApplyPlatformTenantSurfaceResponseCertState = Literal["failed", "issued", "none", "pending"]

APPLY_PLATFORM_TENANT_SURFACE_RESPONSE_CERT_STATE_VALUES: set[ApplyPlatformTenantSurfaceResponseCertState] = {
    "failed",
    "issued",
    "none",
    "pending",
}


def check_apply_platform_tenant_surface_response_cert_state(value: str) -> ApplyPlatformTenantSurfaceResponseCertState:
    if value in APPLY_PLATFORM_TENANT_SURFACE_RESPONSE_CERT_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLY_PLATFORM_TENANT_SURFACE_RESPONSE_CERT_STATE_VALUES!r}"
    )
