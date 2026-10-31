from typing import Literal

EdgeProtectionResponseRange = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

EDGE_PROTECTION_RESPONSE_RANGE_VALUES: set[EdgeProtectionResponseRange] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_edge_protection_response_range(value: str) -> EdgeProtectionResponseRange:
    if value in EDGE_PROTECTION_RESPONSE_RANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_PROTECTION_RESPONSE_RANGE_VALUES!r}")
