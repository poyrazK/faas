from typing import Literal

SLOResponseWindowDays = Literal[7, 30]

SLO_RESPONSE_WINDOW_DAYS_VALUES: set[SLOResponseWindowDays] = {
    7,
    30,
}


def check_slo_response_window_days(value: int) -> SLOResponseWindowDays:
    if value in SLO_RESPONSE_WINDOW_DAYS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SLO_RESPONSE_WINDOW_DAYS_VALUES!r}")
