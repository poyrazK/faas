from typing import Literal

ObsRecordingCoverageState = Literal["covered", "degraded", "partial", "unknown"]

OBS_RECORDING_COVERAGE_STATE_VALUES: set[ObsRecordingCoverageState] = {
    "covered",
    "degraded",
    "partial",
    "unknown",
}


def check_obs_recording_coverage_state(value: str) -> ObsRecordingCoverageState:
    if value in OBS_RECORDING_COVERAGE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RECORDING_COVERAGE_STATE_VALUES!r}")
