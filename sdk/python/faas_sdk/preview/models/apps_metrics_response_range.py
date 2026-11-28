from typing import Literal

AppsMetricsResponseRange = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

APPS_METRICS_RESPONSE_RANGE_VALUES: set[AppsMetricsResponseRange] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_apps_metrics_response_range(value: str) -> AppsMetricsResponseRange:
    if value in APPS_METRICS_RESPONSE_RANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPS_METRICS_RESPONSE_RANGE_VALUES!r}")
