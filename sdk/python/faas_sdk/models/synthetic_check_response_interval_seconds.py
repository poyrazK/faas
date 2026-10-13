from typing import Literal

SyntheticCheckResponseIntervalSeconds = Literal[300, 900, 3600]

SYNTHETIC_CHECK_RESPONSE_INTERVAL_SECONDS_VALUES: set[SyntheticCheckResponseIntervalSeconds] = {
    300,
    900,
    3600,
}


def check_synthetic_check_response_interval_seconds(value: int) -> SyntheticCheckResponseIntervalSeconds:
    if value in SYNTHETIC_CHECK_RESPONSE_INTERVAL_SECONDS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SYNTHETIC_CHECK_RESPONSE_INTERVAL_SECONDS_VALUES!r}")
