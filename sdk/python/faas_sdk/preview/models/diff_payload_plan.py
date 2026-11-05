from typing import Literal

DiffPayloadPlan = Literal["free", "hobby", "pro", "scale"]

DIFF_PAYLOAD_PLAN_VALUES: set[DiffPayloadPlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_diff_payload_plan(value: str) -> DiffPayloadPlan:
    if value in DIFF_PAYLOAD_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DIFF_PAYLOAD_PLAN_VALUES!r}")
