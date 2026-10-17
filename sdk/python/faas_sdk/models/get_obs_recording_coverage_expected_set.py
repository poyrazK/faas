from typing import Literal

GetObsRecordingCoverageExpectedSet = Literal["gregale-core-v1", "gregale-full-v1"]

GET_OBS_RECORDING_COVERAGE_EXPECTED_SET_VALUES: set[GetObsRecordingCoverageExpectedSet] = {
    "gregale-core-v1",
    "gregale-full-v1",
}


def check_get_obs_recording_coverage_expected_set(value: str) -> GetObsRecordingCoverageExpectedSet:
    if value in GET_OBS_RECORDING_COVERAGE_EXPECTED_SET_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_OBS_RECORDING_COVERAGE_EXPECTED_SET_VALUES!r}")
