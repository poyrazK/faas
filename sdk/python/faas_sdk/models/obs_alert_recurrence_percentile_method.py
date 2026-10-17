from typing import Literal

ObsAlertRecurrencePercentileMethod = Literal["nearest_rank_whole_seconds"]

OBS_ALERT_RECURRENCE_PERCENTILE_METHOD_VALUES: set[ObsAlertRecurrencePercentileMethod] = {
    "nearest_rank_whole_seconds",
}


def check_obs_alert_recurrence_percentile_method(value: str) -> ObsAlertRecurrencePercentileMethod:
    if value in OBS_ALERT_RECURRENCE_PERCENTILE_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_RECURRENCE_PERCENTILE_METHOD_VALUES!r}")
