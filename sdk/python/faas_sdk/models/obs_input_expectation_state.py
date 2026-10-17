from typing import Literal

ObsInputExpectationState = Literal["degraded", "healthy", "unknown"]

OBS_INPUT_EXPECTATION_STATE_VALUES: set[ObsInputExpectationState] = {
    "degraded",
    "healthy",
    "unknown",
}


def check_obs_input_expectation_state(value: str) -> ObsInputExpectationState:
    if value in OBS_INPUT_EXPECTATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_EXPECTATION_STATE_VALUES!r}")
