from typing import Literal

PlatformTenantPlanRequestTierMode = Literal["graduated", "volume"]

PLATFORM_TENANT_PLAN_REQUEST_TIER_MODE_VALUES: set[PlatformTenantPlanRequestTierMode] = {
    "graduated",
    "volume",
}


def check_platform_tenant_plan_request_tier_mode(value: str) -> PlatformTenantPlanRequestTierMode:
    if value in PLATFORM_TENANT_PLAN_REQUEST_TIER_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_PLAN_REQUEST_TIER_MODE_VALUES!r}")
