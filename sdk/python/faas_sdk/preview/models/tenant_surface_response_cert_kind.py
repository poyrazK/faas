from typing import Literal

TenantSurfaceResponseCertKind = Literal["per_host_san"]

TENANT_SURFACE_RESPONSE_CERT_KIND_VALUES: set[TenantSurfaceResponseCertKind] = {
    "per_host_san",
}


def check_tenant_surface_response_cert_kind(value: str) -> TenantSurfaceResponseCertKind:
    if value in TENANT_SURFACE_RESPONSE_CERT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TENANT_SURFACE_RESPONSE_CERT_KIND_VALUES!r}")
