from typing import Literal

AccountLimitsPlan = Literal["free", "hobby", "pro", "scale"]

ACCOUNT_LIMITS_PLAN_VALUES: set[AccountLimitsPlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_account_limits_plan(value: str) -> AccountLimitsPlan:
    if value in ACCOUNT_LIMITS_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_LIMITS_PLAN_VALUES!r}")
