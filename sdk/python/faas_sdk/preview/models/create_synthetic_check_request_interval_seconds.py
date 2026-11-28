from typing import Literal

CreateSyntheticCheckRequestIntervalSeconds = Literal[300, 900, 3600]

CREATE_SYNTHETIC_CHECK_REQUEST_INTERVAL_SECONDS_VALUES: set[CreateSyntheticCheckRequestIntervalSeconds] = {
    300,
    900,
    3600,
}


def check_create_synthetic_check_request_interval_seconds(value: int) -> CreateSyntheticCheckRequestIntervalSeconds:
    if value in CREATE_SYNTHETIC_CHECK_REQUEST_INTERVAL_SECONDS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_SYNTHETIC_CHECK_REQUEST_INTERVAL_SECONDS_VALUES!r}"
    )
