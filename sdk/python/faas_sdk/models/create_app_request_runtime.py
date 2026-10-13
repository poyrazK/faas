from typing import Literal

CreateAppRequestRuntime = Literal["binary", "go124", "go124-alpine", "node22", "node24", "python312", "python313"]

CREATE_APP_REQUEST_RUNTIME_VALUES: set[CreateAppRequestRuntime] = {
    "binary",
    "go124",
    "go124-alpine",
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_create_app_request_runtime(value: str) -> CreateAppRequestRuntime:
    if value in CREATE_APP_REQUEST_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_RUNTIME_VALUES!r}")
