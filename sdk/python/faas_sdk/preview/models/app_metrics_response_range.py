from typing import Literal

AppMetricsResponseRange = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

APP_METRICS_RESPONSE_RANGE_VALUES: set[AppMetricsResponseRange] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_app_metrics_response_range(value: str) -> AppMetricsResponseRange:
    if value in APP_METRICS_RESPONSE_RANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_METRICS_RESPONSE_RANGE_VALUES!r}")
