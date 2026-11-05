from typing import Literal

PasswordLoginResponsePlan = Literal["free", "hobby", "pro", "scale"]

PASSWORD_LOGIN_RESPONSE_PLAN_VALUES: set[PasswordLoginResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_password_login_response_plan(value: str) -> PasswordLoginResponsePlan:
    if value in PASSWORD_LOGIN_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PASSWORD_LOGIN_RESPONSE_PLAN_VALUES!r}")
