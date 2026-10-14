from typing import Literal

UpdateAppRequestReleasePolicyType1 = Literal["immediate", "safe"]

UPDATE_APP_REQUEST_RELEASE_POLICY_TYPE_1_VALUES: set[UpdateAppRequestReleasePolicyType1] = {
    "immediate",
    "safe",
}


def check_update_app_request_release_policy_type_1(value: str) -> UpdateAppRequestReleasePolicyType1:
    if value in UPDATE_APP_REQUEST_RELEASE_POLICY_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_RELEASE_POLICY_TYPE_1_VALUES!r}")
