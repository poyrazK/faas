from typing import Literal

AppHealthResponseScope = Literal["default"]

APP_HEALTH_RESPONSE_SCOPE_VALUES: set[AppHealthResponseScope] = {
    "default",
}


def check_app_health_response_scope(value: str) -> AppHealthResponseScope:
    if value in APP_HEALTH_RESPONSE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_RESPONSE_SCOPE_VALUES!r}")
