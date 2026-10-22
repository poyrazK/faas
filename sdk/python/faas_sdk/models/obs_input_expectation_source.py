from typing import Literal

ObsInputExpectationSource = Literal["compute_discovery", "explicit_minimum"]

OBS_INPUT_EXPECTATION_SOURCE_VALUES: set[ObsInputExpectationSource] = {
    "compute_discovery",
    "explicit_minimum",
}


def check_obs_input_expectation_source(value: str) -> ObsInputExpectationSource:
    if value in OBS_INPUT_EXPECTATION_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_EXPECTATION_SOURCE_VALUES!r}")
