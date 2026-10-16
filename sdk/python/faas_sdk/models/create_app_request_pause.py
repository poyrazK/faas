from typing import Literal

CreateAppRequestPause = Literal["between_requests", "never", "when_idle"]

CREATE_APP_REQUEST_PAUSE_VALUES: set[CreateAppRequestPause] = {
    "between_requests",
    "never",
    "when_idle",
}


def check_create_app_request_pause(value: str) -> CreateAppRequestPause:
    if value in CREATE_APP_REQUEST_PAUSE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_PAUSE_VALUES!r}")
