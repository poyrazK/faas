from typing import Literal

PlanWorkloadAccess = Literal["key", "public"]

PLAN_WORKLOAD_ACCESS_VALUES: set[PlanWorkloadAccess] = {
    "key",
    "public",
}


def check_plan_workload_access(value: str) -> PlanWorkloadAccess:
    if value in PLAN_WORKLOAD_ACCESS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_WORKLOAD_ACCESS_VALUES!r}")
