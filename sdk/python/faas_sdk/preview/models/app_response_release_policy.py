from typing import Literal

AppResponseReleasePolicy = Literal["fast", "immediate", "safe"]

APP_RESPONSE_RELEASE_POLICY_VALUES: set[AppResponseReleasePolicy] = {
    "fast",
    "immediate",
    "safe",
}


def check_app_response_release_policy(value: str) -> AppResponseReleasePolicy:
    if value in APP_RESPONSE_RELEASE_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_RELEASE_POLICY_VALUES!r}")
