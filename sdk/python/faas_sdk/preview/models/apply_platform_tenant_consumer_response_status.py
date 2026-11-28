from typing import Literal

ApplyPlatformTenantConsumerResponseStatus = Literal["active"]

APPLY_PLATFORM_TENANT_CONSUMER_RESPONSE_STATUS_VALUES: set[ApplyPlatformTenantConsumerResponseStatus] = {
    "active",
}


def check_apply_platform_tenant_consumer_response_status(value: str) -> ApplyPlatformTenantConsumerResponseStatus:
    if value in APPLY_PLATFORM_TENANT_CONSUMER_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLY_PLATFORM_TENANT_CONSUMER_RESPONSE_STATUS_VALUES!r}"
    )
