from typing import Literal

GetObsRuntimeBuildsExpectedRole = Literal["control-plane-v1", "control-plane-v2", "single-box-v1", "single-box-v2"]

GET_OBS_RUNTIME_BUILDS_EXPECTED_ROLE_VALUES: set[GetObsRuntimeBuildsExpectedRole] = {
    "control-plane-v1",
    "control-plane-v2",
    "single-box-v1",
    "single-box-v2",
}


def check_get_obs_runtime_builds_expected_role(value: str) -> GetObsRuntimeBuildsExpectedRole:
    if value in GET_OBS_RUNTIME_BUILDS_EXPECTED_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_OBS_RUNTIME_BUILDS_EXPECTED_ROLE_VALUES!r}")
