from typing import Literal

ObsNotificationHealthState = Literal["degraded", "healthy", "partial"]

OBS_NOTIFICATION_HEALTH_STATE_VALUES: set[ObsNotificationHealthState] = {
    "degraded",
    "healthy",
    "partial",
}


def check_obs_notification_health_state(value: str) -> ObsNotificationHealthState:
    if value in OBS_NOTIFICATION_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_NOTIFICATION_HEALTH_STATE_VALUES!r}")
