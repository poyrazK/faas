from typing import Literal

PlanWorkloadAction = Literal["create", "update"]

PLAN_WORKLOAD_ACTION_VALUES: set[PlanWorkloadAction] = {
    "create",
    "update",
}


def check_plan_workload_action(value: str) -> PlanWorkloadAction:
    if value in PLAN_WORKLOAD_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_WORKLOAD_ACTION_VALUES!r}")
