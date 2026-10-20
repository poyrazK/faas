from typing import Literal

ApplyPlatformTenantSurfaceRequestCertKind = Literal["per_host_san"]

APPLY_PLATFORM_TENANT_SURFACE_REQUEST_CERT_KIND_VALUES: set[ApplyPlatformTenantSurfaceRequestCertKind] = {
    "per_host_san",
}


def check_apply_platform_tenant_surface_request_cert_kind(value: str) -> ApplyPlatformTenantSurfaceRequestCertKind:
    if value in APPLY_PLATFORM_TENANT_SURFACE_REQUEST_CERT_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLY_PLATFORM_TENANT_SURFACE_REQUEST_CERT_KIND_VALUES!r}"
    )
