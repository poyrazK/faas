from typing import Literal

AppSLOResponseWindow = Literal["1h", "24h", "7d"]

APP_SLO_RESPONSE_WINDOW_VALUES: set[AppSLOResponseWindow] = {
    "1h",
    "24h",
    "7d",
}


def check_app_slo_response_window(value: str) -> AppSLOResponseWindow:
    if value in APP_SLO_RESPONSE_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_SLO_RESPONSE_WINDOW_VALUES!r}")
