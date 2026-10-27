from typing import Literal

PlatformTenantSelfHostnameResponseAction = Literal["created", "unchanged"]

PLATFORM_TENANT_SELF_HOSTNAME_RESPONSE_ACTION_VALUES: set[PlatformTenantSelfHostnameResponseAction] = {
    "created",
    "unchanged",
}


def check_platform_tenant_self_hostname_response_action(value: str) -> PlatformTenantSelfHostnameResponseAction:
    if value in PLATFORM_TENANT_SELF_HOSTNAME_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_SELF_HOSTNAME_RESPONSE_ACTION_VALUES!r}"
    )
