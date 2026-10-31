from typing import Literal

ObsInputHealthReason = Literal["expected_role_not_selected", "expected_role_unavailable"]

OBS_INPUT_HEALTH_REASON_VALUES: set[ObsInputHealthReason] = {
    "expected_role_not_selected",
    "expected_role_unavailable",
}


def check_obs_input_health_reason(value: str) -> ObsInputHealthReason:
    if value in OBS_INPUT_HEALTH_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_HEALTH_REASON_VALUES!r}")
