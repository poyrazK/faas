from typing import Literal

GetAppsMetricsRange = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

GET_APPS_METRICS_RANGE_VALUES: set[GetAppsMetricsRange] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_get_apps_metrics_range(value: str) -> GetAppsMetricsRange:
    if value in GET_APPS_METRICS_RANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APPS_METRICS_RANGE_VALUES!r}")
