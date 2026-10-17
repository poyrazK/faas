from typing import Literal

PlatformTenantPlanResponseUnit = Literal["compute_second", "request", "response_megabyte"]

PLATFORM_TENANT_PLAN_RESPONSE_UNIT_VALUES: set[PlatformTenantPlanResponseUnit] = {
    "compute_second",
    "request",
    "response_megabyte",
}


def check_platform_tenant_plan_response_unit(value: str) -> PlatformTenantPlanResponseUnit:
    if value in PLATFORM_TENANT_PLAN_RESPONSE_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_PLAN_RESPONSE_UNIT_VALUES!r}")
