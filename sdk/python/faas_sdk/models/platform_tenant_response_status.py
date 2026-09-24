from typing import Literal

PlatformTenantResponseStatus = Literal["active", "suspended"]

PLATFORM_TENANT_RESPONSE_STATUS_VALUES: set[PlatformTenantResponseStatus] = {
    "active",
    "suspended",
}


def check_platform_tenant_response_status(value: str) -> PlatformTenantResponseStatus:
    if value in PLATFORM_TENANT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_RESPONSE_STATUS_VALUES!r}")
