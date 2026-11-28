from typing import Literal

AppSecurityPostureResponseProfile = Literal["authenticated", "internal", "public"]

APP_SECURITY_POSTURE_RESPONSE_PROFILE_VALUES: set[AppSecurityPostureResponseProfile] = {
    "authenticated",
    "internal",
    "public",
}


def check_app_security_posture_response_profile(value: str) -> AppSecurityPostureResponseProfile:
    if value in APP_SECURITY_POSTURE_RESPONSE_PROFILE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_SECURITY_POSTURE_RESPONSE_PROFILE_VALUES!r}")
