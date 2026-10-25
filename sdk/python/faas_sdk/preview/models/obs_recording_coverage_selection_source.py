from typing import Literal

ObsRecordingCoverageSelectionSource = Literal["explicit_request", "none"]

OBS_RECORDING_COVERAGE_SELECTION_SOURCE_VALUES: set[ObsRecordingCoverageSelectionSource] = {
    "explicit_request",
    "none",
}


def check_obs_recording_coverage_selection_source(value: str) -> ObsRecordingCoverageSelectionSource:
    if value in OBS_RECORDING_COVERAGE_SELECTION_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RECORDING_COVERAGE_SELECTION_SOURCE_VALUES!r}")
