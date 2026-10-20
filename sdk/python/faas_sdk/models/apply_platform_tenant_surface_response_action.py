from typing import Literal

ApplyPlatformTenantSurfaceResponseAction = Literal["create", "link", "unchanged"]

APPLY_PLATFORM_TENANT_SURFACE_RESPONSE_ACTION_VALUES: set[ApplyPlatformTenantSurfaceResponseAction] = {
    "create",
    "link",
    "unchanged",
}


def check_apply_platform_tenant_surface_response_action(value: str) -> ApplyPlatformTenantSurfaceResponseAction:
    if value in APPLY_PLATFORM_TENANT_SURFACE_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLY_PLATFORM_TENANT_SURFACE_RESPONSE_ACTION_VALUES!r}"
    )
