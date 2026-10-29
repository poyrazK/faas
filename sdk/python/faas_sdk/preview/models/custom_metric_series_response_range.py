from typing import Literal

CustomMetricSeriesResponseRange = Literal["15d", "1h", "24h", "6h", "7d"]

CUSTOM_METRIC_SERIES_RESPONSE_RANGE_VALUES: set[CustomMetricSeriesResponseRange] = {
    "15d",
    "1h",
    "24h",
    "6h",
    "7d",
}


def check_custom_metric_series_response_range(value: str) -> CustomMetricSeriesResponseRange:
    if value in CUSTOM_METRIC_SERIES_RESPONSE_RANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CUSTOM_METRIC_SERIES_RESPONSE_RANGE_VALUES!r}")
