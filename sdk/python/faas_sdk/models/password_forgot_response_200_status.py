from typing import Literal

PasswordForgotResponse200Status = Literal["ok"]

PASSWORD_FORGOT_RESPONSE_200_STATUS_VALUES: set[PasswordForgotResponse200Status] = {
    "ok",
}


def check_password_forgot_response_200_status(value: str) -> PasswordForgotResponse200Status:
    if value in PASSWORD_FORGOT_RESPONSE_200_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PASSWORD_FORGOT_RESPONSE_200_STATUS_VALUES!r}")
