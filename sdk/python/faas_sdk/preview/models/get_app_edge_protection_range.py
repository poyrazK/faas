from typing import Literal

GetAppEdgeProtectionRange = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

GET_APP_EDGE_PROTECTION_RANGE_VALUES: set[GetAppEdgeProtectionRange] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_get_app_edge_protection_range(value: str) -> GetAppEdgeProtectionRange:
    if value in GET_APP_EDGE_PROTECTION_RANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_EDGE_PROTECTION_RANGE_VALUES!r}")
