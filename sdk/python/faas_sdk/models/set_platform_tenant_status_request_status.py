from typing import Literal

SetPlatformTenantStatusRequestStatus = Literal["active", "suspended"]

SET_PLATFORM_TENANT_STATUS_REQUEST_STATUS_VALUES: set[SetPlatformTenantStatusRequestStatus] = {
    "active",
    "suspended",
}


def check_set_platform_tenant_status_request_status(value: str) -> SetPlatformTenantStatusRequestStatus:
    if value in SET_PLATFORM_TENANT_STATUS_REQUEST_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SET_PLATFORM_TENANT_STATUS_REQUEST_STATUS_VALUES!r}")
