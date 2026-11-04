from typing import Literal

ApplyPlatformTenantHostnameResponseAction = Literal["create", "unchanged"]

APPLY_PLATFORM_TENANT_HOSTNAME_RESPONSE_ACTION_VALUES: set[ApplyPlatformTenantHostnameResponseAction] = {
    "create",
    "unchanged",
}


def check_apply_platform_tenant_hostname_response_action(value: str) -> ApplyPlatformTenantHostnameResponseAction:
    if value in APPLY_PLATFORM_TENANT_HOSTNAME_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLY_PLATFORM_TENANT_HOSTNAME_RESPONSE_ACTION_VALUES!r}"
    )
