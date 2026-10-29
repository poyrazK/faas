from typing import Literal

AppSecurityPostureResponseSecurityPolicy = Literal["enforce", "off", "warn"]

APP_SECURITY_POSTURE_RESPONSE_SECURITY_POLICY_VALUES: set[AppSecurityPostureResponseSecurityPolicy] = {
    "enforce",
    "off",
    "warn",
}


def check_app_security_posture_response_security_policy(value: str) -> AppSecurityPostureResponseSecurityPolicy:
    if value in APP_SECURITY_POSTURE_RESPONSE_SECURITY_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_SECURITY_POSTURE_RESPONSE_SECURITY_POLICY_VALUES!r}"
    )
