from typing import Literal

PlatformTenantOffboardingPlanResponseStatus = Literal["active", "suspended"]

PLATFORM_TENANT_OFFBOARDING_PLAN_RESPONSE_STATUS_VALUES: set[PlatformTenantOffboardingPlanResponseStatus] = {
    "active",
    "suspended",
}


def check_platform_tenant_offboarding_plan_response_status(value: str) -> PlatformTenantOffboardingPlanResponseStatus:
    if value in PLATFORM_TENANT_OFFBOARDING_PLAN_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_OFFBOARDING_PLAN_RESPONSE_STATUS_VALUES!r}"
    )
