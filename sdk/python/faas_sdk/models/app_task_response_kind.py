from typing import Literal

AppTaskResponseKind = Literal["manual", "release"]

APP_TASK_RESPONSE_KIND_VALUES: set[AppTaskResponseKind] = {
    "manual",
    "release",
}


def check_app_task_response_kind(value: str) -> AppTaskResponseKind:
    if value in APP_TASK_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_TASK_RESPONSE_KIND_VALUES!r}")
