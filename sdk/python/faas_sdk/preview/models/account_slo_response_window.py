from typing import Literal

AccountSLOResponseWindow = Literal["1h", "24h", "7d"]

ACCOUNT_SLO_RESPONSE_WINDOW_VALUES: set[AccountSLOResponseWindow] = {
    "1h",
    "24h",
    "7d",
}


def check_account_slo_response_window(value: str) -> AccountSLOResponseWindow:
    if value in ACCOUNT_SLO_RESPONSE_WINDOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_SLO_RESPONSE_WINDOW_VALUES!r}")
