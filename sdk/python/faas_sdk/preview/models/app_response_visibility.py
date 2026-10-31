from typing import Literal

AppResponseVisibility = Literal["internal", "public"]

APP_RESPONSE_VISIBILITY_VALUES: set[AppResponseVisibility] = {
    "internal",
    "public",
}


def check_app_response_visibility(value: str) -> AppResponseVisibility:
    if value in APP_RESPONSE_VISIBILITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_VISIBILITY_VALUES!r}")
