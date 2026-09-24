from typing import Literal

PlatformTenantActivationResponseStatus = Literal["active", "suspended"]

PLATFORM_TENANT_ACTIVATION_RESPONSE_STATUS_VALUES: set[PlatformTenantActivationResponseStatus] = {
    "active",
    "suspended",
}


def check_platform_tenant_activation_response_status(value: str) -> PlatformTenantActivationResponseStatus:
    if value in PLATFORM_TENANT_ACTIVATION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_ACTIVATION_RESPONSE_STATUS_VALUES!r}"
    )
