from typing import Literal

GetServiceMapRange = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

GET_SERVICE_MAP_RANGE_VALUES: set[GetServiceMapRange] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_get_service_map_range(value: str) -> GetServiceMapRange:
    if value in GET_SERVICE_MAP_RANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_SERVICE_MAP_RANGE_VALUES!r}")
