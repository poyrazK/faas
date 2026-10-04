from typing import Literal

GlobalRuntimeQuotaPolicyPlan = Literal["free", "hobby", "pro", "scale"]

GLOBAL_RUNTIME_QUOTA_POLICY_PLAN_VALUES: set[GlobalRuntimeQuotaPolicyPlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_global_runtime_quota_policy_plan(value: str) -> GlobalRuntimeQuotaPolicyPlan:
    if value in GLOBAL_RUNTIME_QUOTA_POLICY_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GLOBAL_RUNTIME_QUOTA_POLICY_PLAN_VALUES!r}")
