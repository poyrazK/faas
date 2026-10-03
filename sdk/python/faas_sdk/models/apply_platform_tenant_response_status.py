from typing import Literal

ApplyPlatformTenantResponseStatus = Literal["active", "suspended"]

APPLY_PLATFORM_TENANT_RESPONSE_STATUS_VALUES: set[ApplyPlatformTenantResponseStatus] = {
    "active",
    "suspended",
}


def check_apply_platform_tenant_response_status(value: str) -> ApplyPlatformTenantResponseStatus:
    if value in APPLY_PLATFORM_TENANT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLY_PLATFORM_TENANT_RESPONSE_STATUS_VALUES!r}")
