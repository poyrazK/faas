from typing import Literal

UpdateAppRequestPause = Literal["", "between_requests", "never", "when_idle"]

UPDATE_APP_REQUEST_PAUSE_VALUES: set[UpdateAppRequestPause] = {
    "",
    "between_requests",
    "never",
    "when_idle",
}


def check_update_app_request_pause(value: str) -> UpdateAppRequestPause:
    if value in UPDATE_APP_REQUEST_PAUSE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_PAUSE_VALUES!r}")
