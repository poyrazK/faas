from typing import Literal

AppResponseRuntime = Literal["binary", "go124", "go124-alpine", "node22", "node24", "python312", "python313"]

APP_RESPONSE_RUNTIME_VALUES: set[AppResponseRuntime] = {
    "binary",
    "go124",
    "go124-alpine",
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_app_response_runtime(value: str) -> AppResponseRuntime:
    if value in APP_RESPONSE_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_RUNTIME_VALUES!r}")
