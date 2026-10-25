from typing import Literal

DiffResponsePlan = Literal["free", "hobby", "pro", "scale"]

DIFF_RESPONSE_PLAN_VALUES: set[DiffResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_diff_response_plan(value: str) -> DiffResponsePlan:
    if value in DIFF_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DIFF_RESPONSE_PLAN_VALUES!r}")
