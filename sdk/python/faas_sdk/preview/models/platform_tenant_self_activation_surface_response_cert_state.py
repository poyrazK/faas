from typing import Literal

PlatformTenantSelfActivationSurfaceResponseCertState = Literal["failed", "issued", "none", "pending"]

PLATFORM_TENANT_SELF_ACTIVATION_SURFACE_RESPONSE_CERT_STATE_VALUES: set[
    PlatformTenantSelfActivationSurfaceResponseCertState
] = {
    "failed",
    "issued",
    "none",
    "pending",
}


def check_platform_tenant_self_activation_surface_response_cert_state(
    value: str,
) -> PlatformTenantSelfActivationSurfaceResponseCertState:
    if value in PLATFORM_TENANT_SELF_ACTIVATION_SURFACE_RESPONSE_CERT_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_SELF_ACTIVATION_SURFACE_RESPONSE_CERT_STATE_VALUES!r}"
    )
