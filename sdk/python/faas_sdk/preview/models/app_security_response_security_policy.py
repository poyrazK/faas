from typing import Literal

AppSecurityResponseSecurityPolicy = Literal["enforce", "off", "warn"]

APP_SECURITY_RESPONSE_SECURITY_POLICY_VALUES: set[AppSecurityResponseSecurityPolicy] = {
    "enforce",
    "off",
    "warn",
}


def check_app_security_response_security_policy(value: str) -> AppSecurityResponseSecurityPolicy:
    if value in APP_SECURITY_RESPONSE_SECURITY_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_SECURITY_RESPONSE_SECURITY_POLICY_VALUES!r}")
