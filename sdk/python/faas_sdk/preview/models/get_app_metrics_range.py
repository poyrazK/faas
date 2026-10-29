from typing import Literal

GetAppMetricsRange = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

GET_APP_METRICS_RANGE_VALUES: set[GetAppMetricsRange] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_get_app_metrics_range(value: str) -> GetAppMetricsRange:
    if value in GET_APP_METRICS_RANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_METRICS_RANGE_VALUES!r}")
