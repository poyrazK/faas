from typing import Literal

CanaryProfileSignalMetric = Literal["cpu_per_request", "cpu_per_second"]

CANARY_PROFILE_SIGNAL_METRIC_VALUES: set[CanaryProfileSignalMetric] = {
    "cpu_per_request",
    "cpu_per_second",
}


def check_canary_profile_signal_metric(value: str) -> CanaryProfileSignalMetric:
    if value in CANARY_PROFILE_SIGNAL_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CANARY_PROFILE_SIGNAL_METRIC_VALUES!r}")
