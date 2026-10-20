from typing import Literal

PlatformTenantSelfConsumerResponseStatus = Literal["active", "revoked"]

PLATFORM_TENANT_SELF_CONSUMER_RESPONSE_STATUS_VALUES: set[PlatformTenantSelfConsumerResponseStatus] = {
    "active",
    "revoked",
}


def check_platform_tenant_self_consumer_response_status(value: str) -> PlatformTenantSelfConsumerResponseStatus:
    if value in PLATFORM_TENANT_SELF_CONSUMER_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_SELF_CONSUMER_RESPONSE_STATUS_VALUES!r}"
    )
