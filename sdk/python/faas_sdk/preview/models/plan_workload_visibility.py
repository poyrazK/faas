from typing import Literal

PlanWorkloadVisibility = Literal["internal"]

PLAN_WORKLOAD_VISIBILITY_VALUES: set[PlanWorkloadVisibility] = {
    "internal",
}


def check_plan_workload_visibility(value: str) -> PlanWorkloadVisibility:
    if value in PLAN_WORKLOAD_VISIBILITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_WORKLOAD_VISIBILITY_VALUES!r}")
