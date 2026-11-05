from typing import Literal

ProgrammaticAuthResponsePlan = Literal["free", "hobby", "pro", "scale"]

PROGRAMMATIC_AUTH_RESPONSE_PLAN_VALUES: set[ProgrammaticAuthResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_programmatic_auth_response_plan(value: str) -> ProgrammaticAuthResponsePlan:
    if value in PROGRAMMATIC_AUTH_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROGRAMMATIC_AUTH_RESPONSE_PLAN_VALUES!r}")
