from typing import Literal

UpdateAppRequestRestartPolicyType2Type1 = Literal["always", "no", "on-failure", "unless-stopped"]

UPDATE_APP_REQUEST_RESTART_POLICY_TYPE_2_TYPE_1_VALUES: set[UpdateAppRequestRestartPolicyType2Type1] = {
    "always",
    "no",
    "on-failure",
    "unless-stopped",
}


def check_update_app_request_restart_policy_type_2_type_1(value: str) -> UpdateAppRequestRestartPolicyType2Type1:
    if value in UPDATE_APP_REQUEST_RESTART_POLICY_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_RESTART_POLICY_TYPE_2_TYPE_1_VALUES!r}"
    )
