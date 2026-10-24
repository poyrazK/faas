from typing import Literal

SeatUsageResponsePlan = Literal["free", "hobby", "pro", "scale"]

SEAT_USAGE_RESPONSE_PLAN_VALUES: set[SeatUsageResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_seat_usage_response_plan(value: str) -> SeatUsageResponsePlan:
    if value in SEAT_USAGE_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SEAT_USAGE_RESPONSE_PLAN_VALUES!r}")
