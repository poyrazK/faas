from typing import Literal

GetAccountSLOWindow = Literal["1h", "24h", "7d"]

GET_ACCOUNT_SLO_WINDOW_VALUES: set[GetAccountSLOWindow] = {
    "1h",
    "24h",
    "7d",
}


def check_get_account_slo_window(value: str) -> GetAccountSLOWindow:
    if value in GET_ACCOUNT_SLO_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_ACCOUNT_SLO_WINDOW_VALUES!r}")
