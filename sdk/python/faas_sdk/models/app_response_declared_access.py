from typing import Literal

AppResponseDeclaredAccess = Literal["key", "public"]

APP_RESPONSE_DECLARED_ACCESS_VALUES: set[AppResponseDeclaredAccess] = {
    "key",
    "public",
}


def check_app_response_declared_access(value: str) -> AppResponseDeclaredAccess:
    if value in APP_RESPONSE_DECLARED_ACCESS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_DECLARED_ACCESS_VALUES!r}")
