from typing import Literal

CreateAppRequestRuntime = Literal["go124", "node22", "python312"]

CREATE_APP_REQUEST_RUNTIME_VALUES: set[CreateAppRequestRuntime] = {
    "go124",
    "node22",
    "python312",
}


def check_create_app_request_runtime(value: str) -> CreateAppRequestRuntime:
    if value in CREATE_APP_REQUEST_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_RUNTIME_VALUES!r}")
