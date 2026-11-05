from typing import Literal

PlatformTenantReconciliationPlanChangeResourceType = Literal["consumer", "hostname", "surface"]

PLATFORM_TENANT_RECONCILIATION_PLAN_CHANGE_RESOURCE_TYPE_VALUES: set[
    PlatformTenantReconciliationPlanChangeResourceType
] = {
    "consumer",
    "hostname",
    "surface",
}


def check_platform_tenant_reconciliation_plan_change_resource_type(
    value: str,
) -> PlatformTenantReconciliationPlanChangeResourceType:
    if value in PLATFORM_TENANT_RECONCILIATION_PLAN_CHANGE_RESOURCE_TYPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_RECONCILIATION_PLAN_CHANGE_RESOURCE_TYPE_VALUES!r}"
    )
