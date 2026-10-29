from typing import Literal

AccountResponsePlan = Literal["free", "hobby", "pro", "scale"]

ACCOUNT_RESPONSE_PLAN_VALUES: set[AccountResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_account_response_plan(value: str) -> AccountResponsePlan:
    if value in ACCOUNT_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_RESPONSE_PLAN_VALUES!r}")
