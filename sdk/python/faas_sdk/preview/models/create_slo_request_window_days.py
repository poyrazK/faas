from typing import Literal

CreateSLORequestWindowDays = Literal[7, 30]

CREATE_SLO_REQUEST_WINDOW_DAYS_VALUES: set[CreateSLORequestWindowDays] = {
    7,
    30,
}


def check_create_slo_request_window_days(value: int) -> CreateSLORequestWindowDays:
    if value in CREATE_SLO_REQUEST_WINDOW_DAYS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_SLO_REQUEST_WINDOW_DAYS_VALUES!r}")
