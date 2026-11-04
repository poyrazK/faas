from typing import Literal

ObsInputMetricState = Literal["degraded", "healthy", "not_applicable", "unknown"]

OBS_INPUT_METRIC_STATE_VALUES: set[ObsInputMetricState] = {
    "degraded",
    "healthy",
    "not_applicable",
    "unknown",
}


def check_obs_input_metric_state(value: str) -> ObsInputMetricState:
    if value in OBS_INPUT_METRIC_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_METRIC_STATE_VALUES!r}")
