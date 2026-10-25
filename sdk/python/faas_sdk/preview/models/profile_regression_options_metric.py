from typing import Literal

ProfileRegressionOptionsMetric = Literal["cpu_per_request", "cpu_per_second"]

PROFILE_REGRESSION_OPTIONS_METRIC_VALUES: set[ProfileRegressionOptionsMetric] = {
    "cpu_per_request",
    "cpu_per_second",
}


def check_profile_regression_options_metric(value: str) -> ProfileRegressionOptionsMetric:
    if value in PROFILE_REGRESSION_OPTIONS_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_REGRESSION_OPTIONS_METRIC_VALUES!r}")
