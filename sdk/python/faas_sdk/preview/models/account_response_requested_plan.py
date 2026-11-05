from typing import Literal

AccountResponseRequestedPlan = Literal["free", "hobby", "pro", "scale"]

ACCOUNT_RESPONSE_REQUESTED_PLAN_VALUES: set[AccountResponseRequestedPlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_account_response_requested_plan(value: str) -> AccountResponseRequestedPlan:
    if value in ACCOUNT_RESPONSE_REQUESTED_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_RESPONSE_REQUESTED_PLAN_VALUES!r}")
