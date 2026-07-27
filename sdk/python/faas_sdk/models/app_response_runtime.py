from typing import Literal

AppResponseRuntime = Literal["go124", "node22", "python312"]

APP_RESPONSE_RUNTIME_VALUES: set[AppResponseRuntime] = {
    "go124",
    "node22",
    "python312",
}


def check_app_response_runtime(value: str) -> AppResponseRuntime:
    if value in APP_RESPONSE_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_RUNTIME_VALUES!r}")
