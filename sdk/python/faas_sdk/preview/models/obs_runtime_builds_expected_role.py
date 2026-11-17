from typing import Literal

ObsRuntimeBuildsExpectedRole = Literal["", "control-plane-v1", "control-plane-v2", "single-box-v1", "single-box-v2"]

OBS_RUNTIME_BUILDS_EXPECTED_ROLE_VALUES: set[ObsRuntimeBuildsExpectedRole] = {
    "",
    "control-plane-v1",
    "control-plane-v2",
    "single-box-v1",
    "single-box-v2",
}


def check_obs_runtime_builds_expected_role(value: str) -> ObsRuntimeBuildsExpectedRole:
    if value in OBS_RUNTIME_BUILDS_EXPECTED_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDS_EXPECTED_ROLE_VALUES!r}")
