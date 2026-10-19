from typing import Literal

ObsAlertRecurrenceScope = Literal["retained_activation_cohort"]

OBS_ALERT_RECURRENCE_SCOPE_VALUES: set[ObsAlertRecurrenceScope] = {
    "retained_activation_cohort",
}


def check_obs_alert_recurrence_scope(value: str) -> ObsAlertRecurrenceScope:
    if value in OBS_ALERT_RECURRENCE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_RECURRENCE_SCOPE_VALUES!r}")
