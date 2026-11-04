from typing import Literal

CreateAppRequestType = Literal["app", "function"]

CREATE_APP_REQUEST_TYPE_VALUES: set[CreateAppRequestType] = {
    "app",
    "function",
}


def check_create_app_request_type(value: str) -> CreateAppRequestType:
    if value in CREATE_APP_REQUEST_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_TYPE_VALUES!r}")
