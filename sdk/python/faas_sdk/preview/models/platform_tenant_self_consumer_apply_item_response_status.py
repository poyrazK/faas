from typing import Literal

PlatformTenantSelfConsumerApplyItemResponseStatus = Literal["active"]

PLATFORM_TENANT_SELF_CONSUMER_APPLY_ITEM_RESPONSE_STATUS_VALUES: set[
    PlatformTenantSelfConsumerApplyItemResponseStatus
] = {
    "active",
}


def check_platform_tenant_self_consumer_apply_item_response_status(
    value: str,
) -> PlatformTenantSelfConsumerApplyItemResponseStatus:
    if value in PLATFORM_TENANT_SELF_CONSUMER_APPLY_ITEM_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_SELF_CONSUMER_APPLY_ITEM_RESPONSE_STATUS_VALUES!r}"
    )
