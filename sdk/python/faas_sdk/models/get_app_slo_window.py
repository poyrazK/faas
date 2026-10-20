from typing import Literal

GetAppSLOWindow = Literal["1h", "24h", "7d"]

GET_APP_SLO_WINDOW_VALUES: set[GetAppSLOWindow] = {
    "1h",
    "24h",
    "7d",
}


def check_get_app_slo_window(value: str) -> GetAppSLOWindow:
    if value in GET_APP_SLO_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_SLO_WINDOW_VALUES!r}")
