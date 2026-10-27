from typing import Literal

ChangePlanRequestPlan = Literal["free", "hobby", "pro", "scale"]

CHANGE_PLAN_REQUEST_PLAN_VALUES: set[ChangePlanRequestPlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_change_plan_request_plan(value: str) -> ChangePlanRequestPlan:
    if value in CHANGE_PLAN_REQUEST_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CHANGE_PLAN_REQUEST_PLAN_VALUES!r}")
