from typing import Literal

TenantSurfaceResponseCertState = Literal["failed", "issued", "none", "pending", "renewing"]

TENANT_SURFACE_RESPONSE_CERT_STATE_VALUES: set[TenantSurfaceResponseCertState] = {
    "failed",
    "issued",
    "none",
    "pending",
    "renewing",
}


def check_tenant_surface_response_cert_state(value: str) -> TenantSurfaceResponseCertState:
    if value in TENANT_SURFACE_RESPONSE_CERT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TENANT_SURFACE_RESPONSE_CERT_STATE_VALUES!r}")
