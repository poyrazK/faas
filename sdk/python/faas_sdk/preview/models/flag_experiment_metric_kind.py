from typing import Literal

FlagExperimentMetricKind = Literal["conversion", "funnel", "mean", "percentile", "ratio", "retention"]

FLAG_EXPERIMENT_METRIC_KIND_VALUES: set[FlagExperimentMetricKind] = {
    "conversion",
    "funnel",
    "mean",
    "percentile",
    "ratio",
    "retention",
}


def check_flag_experiment_metric_kind(value: str) -> FlagExperimentMetricKind:
    if value in FLAG_EXPERIMENT_METRIC_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FLAG_EXPERIMENT_METRIC_KIND_VALUES!r}")
