from typing import Literal

ApplyPlatformTenantResponseAction = Literal["create", "unchanged"]

APPLY_PLATFORM_TENANT_RESPONSE_ACTION_VALUES: set[ApplyPlatformTenantResponseAction] = {
    "create",
    "unchanged",
}


def check_apply_platform_tenant_response_action(value: str) -> ApplyPlatformTenantResponseAction:
    if value in APPLY_PLATFORM_TENANT_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLY_PLATFORM_TENANT_RESPONSE_ACTION_VALUES!r}")
