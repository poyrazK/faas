from typing import Literal

GetCustomMetricSeriesRange = Literal["15d", "1h", "24h", "6h", "7d"]

GET_CUSTOM_METRIC_SERIES_RANGE_VALUES: set[GetCustomMetricSeriesRange] = {
    "15d",
    "1h",
    "24h",
    "6h",
    "7d",
}


def check_get_custom_metric_series_range(value: str) -> GetCustomMetricSeriesRange:
    if value in GET_CUSTOM_METRIC_SERIES_RANGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_CUSTOM_METRIC_SERIES_RANGE_VALUES!r}")
