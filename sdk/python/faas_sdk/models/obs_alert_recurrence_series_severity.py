from typing import Literal

ObsAlertRecurrenceSeriesSeverity = Literal["critical", "error", "warning"]

OBS_ALERT_RECURRENCE_SERIES_SEVERITY_VALUES: set[ObsAlertRecurrenceSeriesSeverity] = {
    "critical",
    "error",
    "warning",
}


def check_obs_alert_recurrence_series_severity(value: str) -> ObsAlertRecurrenceSeriesSeverity:
    if value in OBS_ALERT_RECURRENCE_SERIES_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_RECURRENCE_SERIES_SEVERITY_VALUES!r}")
