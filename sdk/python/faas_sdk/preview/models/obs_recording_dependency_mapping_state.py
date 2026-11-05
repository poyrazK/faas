from typing import Literal

ObsRecordingDependencyMappingState = Literal["complete", "partial"]

OBS_RECORDING_DEPENDENCY_MAPPING_STATE_VALUES: set[ObsRecordingDependencyMappingState] = {
    "complete",
    "partial",
}


def check_obs_recording_dependency_mapping_state(value: str) -> ObsRecordingDependencyMappingState:
    if value in OBS_RECORDING_DEPENDENCY_MAPPING_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RECORDING_DEPENDENCY_MAPPING_STATE_VALUES!r}")
