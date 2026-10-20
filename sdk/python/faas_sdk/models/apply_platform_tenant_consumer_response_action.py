from typing import Literal

ApplyPlatformTenantConsumerResponseAction = Literal["create", "link", "unchanged"]

APPLY_PLATFORM_TENANT_CONSUMER_RESPONSE_ACTION_VALUES: set[ApplyPlatformTenantConsumerResponseAction] = {
    "create",
    "link",
    "unchanged",
}


def check_apply_platform_tenant_consumer_response_action(value: str) -> ApplyPlatformTenantConsumerResponseAction:
    if value in APPLY_PLATFORM_TENANT_CONSUMER_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLY_PLATFORM_TENANT_CONSUMER_RESPONSE_ACTION_VALUES!r}"
    )
