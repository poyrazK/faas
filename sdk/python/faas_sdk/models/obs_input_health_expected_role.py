from typing import Literal

ObsInputHealthExpectedRole = Literal["", "control-plane-v1", "single-box-v1"]

OBS_INPUT_HEALTH_EXPECTED_ROLE_VALUES: set[ObsInputHealthExpectedRole] = {
    "",
    "control-plane-v1",
    "single-box-v1",
}


def check_obs_input_health_expected_role(value: str) -> ObsInputHealthExpectedRole:
    if value in OBS_INPUT_HEALTH_EXPECTED_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_HEALTH_EXPECTED_ROLE_VALUES!r}")
