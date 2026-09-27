from typing import Literal

GetAppPreAuthObservationsRange = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

GET_APP_PRE_AUTH_OBSERVATIONS_RANGE_VALUES: set[GetAppPreAuthObservationsRange] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_get_app_pre_auth_observations_range(value: str) -> GetAppPreAuthObservationsRange:
    if value in GET_APP_PRE_AUTH_OBSERVATIONS_RANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_PRE_AUTH_OBSERVATIONS_RANGE_VALUES!r}")
