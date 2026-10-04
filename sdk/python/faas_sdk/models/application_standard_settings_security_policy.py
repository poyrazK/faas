from typing import Literal

ApplicationStandardSettingsSecurityPolicy = Literal["enforce", "off", "warn"]

APPLICATION_STANDARD_SETTINGS_SECURITY_POLICY_VALUES: set[ApplicationStandardSettingsSecurityPolicy] = {
    "enforce",
    "off",
    "warn",
}


def check_application_standard_settings_security_policy(value: str) -> ApplicationStandardSettingsSecurityPolicy:
    if value in APPLICATION_STANDARD_SETTINGS_SECURITY_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_SETTINGS_SECURITY_POLICY_VALUES!r}"
    )
