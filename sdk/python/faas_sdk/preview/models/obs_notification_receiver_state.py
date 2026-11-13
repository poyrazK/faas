from typing import Literal

ObsNotificationReceiverState = Literal["degraded", "healthy", "partial"]

OBS_NOTIFICATION_RECEIVER_STATE_VALUES: set[ObsNotificationReceiverState] = {
    "degraded",
    "healthy",
    "partial",
}


def check_obs_notification_receiver_state(value: str) -> ObsNotificationReceiverState:
    if value in OBS_NOTIFICATION_RECEIVER_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_NOTIFICATION_RECEIVER_STATE_VALUES!r}")
