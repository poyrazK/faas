from typing import Literal

CustomMetricSeriesResponseKind = Literal["counter", "gauge"]

CUSTOM_METRIC_SERIES_RESPONSE_KIND_VALUES: set[CustomMetricSeriesResponseKind] = {
    "counter",
    "gauge",
}


def check_custom_metric_series_response_kind(value: str) -> CustomMetricSeriesResponseKind:
    if value in CUSTOM_METRIC_SERIES_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CUSTOM_METRIC_SERIES_RESPONSE_KIND_VALUES!r}")
