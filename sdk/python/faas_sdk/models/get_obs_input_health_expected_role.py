from typing import Literal

GetObsInputHealthExpectedRole = Literal["control-plane-v1", "single-box-v1"]

GET_OBS_INPUT_HEALTH_EXPECTED_ROLE_VALUES: set[GetObsInputHealthExpectedRole] = {
    "control-plane-v1",
    "single-box-v1",
}


def check_get_obs_input_health_expected_role(value: str) -> GetObsInputHealthExpectedRole:
    if value in GET_OBS_INPUT_HEALTH_EXPECTED_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_OBS_INPUT_HEALTH_EXPECTED_ROLE_VALUES!r}")
