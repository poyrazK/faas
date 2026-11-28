from typing import Literal

AppSecurityRequestSecurityPolicy = Literal["enforce", "off", "warn"]

APP_SECURITY_REQUEST_SECURITY_POLICY_VALUES: set[AppSecurityRequestSecurityPolicy] = {
    "enforce",
    "off",
    "warn",
}


def check_app_security_request_security_policy(value: str) -> AppSecurityRequestSecurityPolicy:
    if value in APP_SECURITY_REQUEST_SECURITY_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_SECURITY_REQUEST_SECURITY_POLICY_VALUES!r}")
