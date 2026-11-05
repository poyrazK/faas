from typing import Literal

CreateTenantSurfaceRequestCertKind = Literal["per_host_san"]

CREATE_TENANT_SURFACE_REQUEST_CERT_KIND_VALUES: set[CreateTenantSurfaceRequestCertKind] = {
    "per_host_san",
}


def check_create_tenant_surface_request_cert_kind(value: str) -> CreateTenantSurfaceRequestCertKind:
    if value in CREATE_TENANT_SURFACE_REQUEST_CERT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_TENANT_SURFACE_REQUEST_CERT_KIND_VALUES!r}")
