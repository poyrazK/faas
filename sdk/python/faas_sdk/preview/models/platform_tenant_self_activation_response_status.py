from typing import Literal

PlatformTenantSelfActivationResponseStatus = Literal["active", "suspended"]

PLATFORM_TENANT_SELF_ACTIVATION_RESPONSE_STATUS_VALUES: set[PlatformTenantSelfActivationResponseStatus] = {
    "active",
    "suspended",
}


def check_platform_tenant_self_activation_response_status(value: str) -> PlatformTenantSelfActivationResponseStatus:
    if value in PLATFORM_TENANT_SELF_ACTIVATION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_SELF_ACTIVATION_RESPONSE_STATUS_VALUES!r}"
    )
