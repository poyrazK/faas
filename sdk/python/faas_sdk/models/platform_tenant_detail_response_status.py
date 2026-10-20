from typing import Literal

PlatformTenantDetailResponseStatus = Literal["active", "suspended"]

PLATFORM_TENANT_DETAIL_RESPONSE_STATUS_VALUES: set[PlatformTenantDetailResponseStatus] = {
    "active",
    "suspended",
}


def check_platform_tenant_detail_response_status(value: str) -> PlatformTenantDetailResponseStatus:
    if value in PLATFORM_TENANT_DETAIL_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_DETAIL_RESPONSE_STATUS_VALUES!r}")
