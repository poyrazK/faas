from typing import Literal

CreateAppRequestVisibility = Literal["internal", "public"]

CREATE_APP_REQUEST_VISIBILITY_VALUES: set[CreateAppRequestVisibility] = {
    "internal",
    "public",
}


def check_create_app_request_visibility(value: str) -> CreateAppRequestVisibility:
    if value in CREATE_APP_REQUEST_VISIBILITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_VISIBILITY_VALUES!r}")
