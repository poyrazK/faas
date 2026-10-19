from typing import Literal

ObsInputExpectationReason = Literal[
    "discovery_invalid", "discovery_invalid_targets", "discovery_missing", "discovery_stale", "discovery_unavailable"
]

OBS_INPUT_EXPECTATION_REASON_VALUES: set[ObsInputExpectationReason] = {
    "discovery_invalid",
    "discovery_invalid_targets",
    "discovery_missing",
    "discovery_stale",
    "discovery_unavailable",
}


def check_obs_input_expectation_reason(value: str) -> ObsInputExpectationReason:
    if value in OBS_INPUT_EXPECTATION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_EXPECTATION_REASON_VALUES!r}")
