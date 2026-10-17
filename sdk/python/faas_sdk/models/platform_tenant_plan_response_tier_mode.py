from typing import Literal

PlatformTenantPlanResponseTierMode = Literal["graduated", "volume"]

PLATFORM_TENANT_PLAN_RESPONSE_TIER_MODE_VALUES: set[PlatformTenantPlanResponseTierMode] = {
    "graduated",
    "volume",
}


def check_platform_tenant_plan_response_tier_mode(value: str) -> PlatformTenantPlanResponseTierMode:
    if value in PLATFORM_TENANT_PLAN_RESPONSE_TIER_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_PLAN_RESPONSE_TIER_MODE_VALUES!r}")
