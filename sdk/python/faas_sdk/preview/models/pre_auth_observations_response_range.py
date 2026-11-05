from typing import Literal

PreAuthObservationsResponseRange = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

PRE_AUTH_OBSERVATIONS_RESPONSE_RANGE_VALUES: set[PreAuthObservationsResponseRange] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_pre_auth_observations_response_range(value: str) -> PreAuthObservationsResponseRange:
    if value in PRE_AUTH_OBSERVATIONS_RESPONSE_RANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PRE_AUTH_OBSERVATIONS_RESPONSE_RANGE_VALUES!r}")
