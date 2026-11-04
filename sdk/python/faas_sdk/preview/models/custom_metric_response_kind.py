from typing import Literal

CustomMetricResponseKind = Literal["counter", "gauge"]

CUSTOM_METRIC_RESPONSE_KIND_VALUES: set[CustomMetricResponseKind] = {
    "counter",
    "gauge",
}


def check_custom_metric_response_kind(value: str) -> CustomMetricResponseKind:
    if value in CUSTOM_METRIC_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CUSTOM_METRIC_RESPONSE_KIND_VALUES!r}")
