from typing import Literal

ObsAlertResponseTimesPercentileMethod = Literal["nearest_rank_whole_seconds"]

OBS_ALERT_RESPONSE_TIMES_PERCENTILE_METHOD_VALUES: set[ObsAlertResponseTimesPercentileMethod] = {
    "nearest_rank_whole_seconds",
}


def check_obs_alert_response_times_percentile_method(value: str) -> ObsAlertResponseTimesPercentileMethod:
    if value in OBS_ALERT_RESPONSE_TIMES_PERCENTILE_METHOD_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_ALERT_RESPONSE_TIMES_PERCENTILE_METHOD_VALUES!r}"
    )
