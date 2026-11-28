from typing import Literal

ObsAlertResponseTimesDurationBasis = Literal["episode_activation"]

OBS_ALERT_RESPONSE_TIMES_DURATION_BASIS_VALUES: set[ObsAlertResponseTimesDurationBasis] = {
    "episode_activation",
}


def check_obs_alert_response_times_duration_basis(value: str) -> ObsAlertResponseTimesDurationBasis:
    if value in OBS_ALERT_RESPONSE_TIMES_DURATION_BASIS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_RESPONSE_TIMES_DURATION_BASIS_VALUES!r}")
