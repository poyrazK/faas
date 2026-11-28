from typing import Literal

ObsNotificationSignalState = Literal["healthy", "no_samples", "stale", "unavailable"]

OBS_NOTIFICATION_SIGNAL_STATE_VALUES: set[ObsNotificationSignalState] = {
    "healthy",
    "no_samples",
    "stale",
    "unavailable",
}


def check_obs_notification_signal_state(value: str) -> ObsNotificationSignalState:
    if value in OBS_NOTIFICATION_SIGNAL_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_NOTIFICATION_SIGNAL_STATE_VALUES!r}")
