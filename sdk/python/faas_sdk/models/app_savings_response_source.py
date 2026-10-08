from typing import Literal

AppSavingsResponseSource = Literal["mixed", "usage_daily", "usage_minutes"]

APP_SAVINGS_RESPONSE_SOURCE_VALUES: set[AppSavingsResponseSource] = {
    "mixed",
    "usage_daily",
    "usage_minutes",
}


def check_app_savings_response_source(value: str) -> AppSavingsResponseSource:
    if value in APP_SAVINGS_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_SAVINGS_RESPONSE_SOURCE_VALUES!r}")
