from typing import Literal

CreateAppRequestRestartPolicy = Literal["always", "no", "on-failure", "unless-stopped"]

CREATE_APP_REQUEST_RESTART_POLICY_VALUES: set[CreateAppRequestRestartPolicy] = {
    "always",
    "no",
    "on-failure",
    "unless-stopped",
}


def check_create_app_request_restart_policy(value: str) -> CreateAppRequestRestartPolicy:
    if value in CREATE_APP_REQUEST_RESTART_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_RESTART_POLICY_VALUES!r}")
