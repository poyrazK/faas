from typing import Literal

PlatformTenantPlanRequestUnit = Literal["compute_second", "request", "response_megabyte"]

PLATFORM_TENANT_PLAN_REQUEST_UNIT_VALUES: set[PlatformTenantPlanRequestUnit] = {
    "compute_second",
    "request",
    "response_megabyte",
}


def check_platform_tenant_plan_request_unit(value: str) -> PlatformTenantPlanRequestUnit:
    if value in PLATFORM_TENANT_PLAN_REQUEST_UNIT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_PLAN_REQUEST_UNIT_VALUES!r}")
