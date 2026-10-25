from typing import Literal

ObsAlertResponseTimesScope = Literal["retained_activation_cohort"]

OBS_ALERT_RESPONSE_TIMES_SCOPE_VALUES: set[ObsAlertResponseTimesScope] = {
    "retained_activation_cohort",
}


def check_obs_alert_response_times_scope(value: str) -> ObsAlertResponseTimesScope:
    if value in OBS_ALERT_RESPONSE_TIMES_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_RESPONSE_TIMES_SCOPE_VALUES!r}")
