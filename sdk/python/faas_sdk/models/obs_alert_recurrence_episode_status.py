from typing import Literal

ObsAlertRecurrenceEpisodeStatus = Literal["firing", "resolved", "unknown"]

OBS_ALERT_RECURRENCE_EPISODE_STATUS_VALUES: set[ObsAlertRecurrenceEpisodeStatus] = {
    "firing",
    "resolved",
    "unknown",
}


def check_obs_alert_recurrence_episode_status(value: str) -> ObsAlertRecurrenceEpisodeStatus:
    if value in OBS_ALERT_RECURRENCE_EPISODE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_ALERT_RECURRENCE_EPISODE_STATUS_VALUES!r}")
