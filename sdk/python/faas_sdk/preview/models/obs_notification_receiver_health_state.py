from typing import Literal

ObsNotificationReceiverHealthState = Literal["degraded", "healthy", "partial"]

OBS_NOTIFICATION_RECEIVER_HEALTH_STATE_VALUES: set[ObsNotificationReceiverHealthState] = {
    "degraded",
    "healthy",
    "partial",
}


def check_obs_notification_receiver_health_state(value: str) -> ObsNotificationReceiverHealthState:
    if value in OBS_NOTIFICATION_RECEIVER_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_NOTIFICATION_RECEIVER_HEALTH_STATE_VALUES!r}")
