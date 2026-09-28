from typing import Literal

PlatformTenantSelfConsumerApplyItemResponseAction = Literal["create", "unchanged"]

PLATFORM_TENANT_SELF_CONSUMER_APPLY_ITEM_RESPONSE_ACTION_VALUES: set[
    PlatformTenantSelfConsumerApplyItemResponseAction
] = {
    "create",
    "unchanged",
}


def check_platform_tenant_self_consumer_apply_item_response_action(
    value: str,
) -> PlatformTenantSelfConsumerApplyItemResponseAction:
    if value in PLATFORM_TENANT_SELF_CONSUMER_APPLY_ITEM_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_SELF_CONSUMER_APPLY_ITEM_RESPONSE_ACTION_VALUES!r}"
    )
