from typing import Literal

CreateAppRequestReleasePolicy = Literal["fast", "immediate", "safe"]

CREATE_APP_REQUEST_RELEASE_POLICY_VALUES: set[CreateAppRequestReleasePolicy] = {
    "fast",
    "immediate",
    "safe",
}


def check_create_app_request_release_policy(value: str) -> CreateAppRequestReleasePolicy:
    if value in CREATE_APP_REQUEST_RELEASE_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_RELEASE_POLICY_VALUES!r}")
