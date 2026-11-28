from typing import Literal

PlanWorkloadTier = Literal["compose", "convention", "single", "unknown", "workspace"]

PLAN_WORKLOAD_TIER_VALUES: set[PlanWorkloadTier] = {
    "compose",
    "convention",
    "single",
    "unknown",
    "workspace",
}


def check_plan_workload_tier(value: str) -> PlanWorkloadTier:
    if value in PLAN_WORKLOAD_TIER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_WORKLOAD_TIER_VALUES!r}")
