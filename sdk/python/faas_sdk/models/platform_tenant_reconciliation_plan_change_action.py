from typing import Literal

PlatformTenantReconciliationPlanChangeAction = Literal["create", "keep", "link", "remove_candidate", "retain_unmanaged"]

PLATFORM_TENANT_RECONCILIATION_PLAN_CHANGE_ACTION_VALUES: set[PlatformTenantReconciliationPlanChangeAction] = {
    "create",
    "keep",
    "link",
    "remove_candidate",
    "retain_unmanaged",
}


def check_platform_tenant_reconciliation_plan_change_action(value: str) -> PlatformTenantReconciliationPlanChangeAction:
    if value in PLATFORM_TENANT_RECONCILIATION_PLAN_CHANGE_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_RECONCILIATION_PLAN_CHANGE_ACTION_VALUES!r}"
    )
