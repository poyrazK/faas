from typing import Literal

QueueStateResponsePlan = Literal["free", "hobby", "pro", "scale"]

QUEUE_STATE_RESPONSE_PLAN_VALUES: set[QueueStateResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_queue_state_response_plan(value: str) -> QueueStateResponsePlan:
    if value in QUEUE_STATE_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {QUEUE_STATE_RESPONSE_PLAN_VALUES!r}")
