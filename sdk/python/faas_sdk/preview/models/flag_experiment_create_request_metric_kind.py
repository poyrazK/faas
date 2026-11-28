from typing import Literal

FlagExperimentCreateRequestMetricKind = Literal["conversion", "funnel", "mean", "percentile", "ratio", "retention"]

FLAG_EXPERIMENT_CREATE_REQUEST_METRIC_KIND_VALUES: set[FlagExperimentCreateRequestMetricKind] = {
    "conversion",
    "funnel",
    "mean",
    "percentile",
    "ratio",
    "retention",
}


def check_flag_experiment_create_request_metric_kind(value: str) -> FlagExperimentCreateRequestMetricKind:
    if value in FLAG_EXPERIMENT_CREATE_REQUEST_METRIC_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_CREATE_REQUEST_METRIC_KIND_VALUES!r}"
    )
