from typing import Literal

PlanManagedNotProvisionedReason = Literal[
    "configured", "not_referenced", "permission", "plan", "quota", "unavailable", "unsupported"
]

PLAN_MANAGED_NOT_PROVISIONED_REASON_VALUES: set[PlanManagedNotProvisionedReason] = {
    "configured",
    "not_referenced",
    "permission",
    "plan",
    "quota",
    "unavailable",
    "unsupported",
}


def check_plan_managed_not_provisioned_reason(value: str) -> PlanManagedNotProvisionedReason:
    if value in PLAN_MANAGED_NOT_PROVISIONED_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_MANAGED_NOT_PROVISIONED_REASON_VALUES!r}")
