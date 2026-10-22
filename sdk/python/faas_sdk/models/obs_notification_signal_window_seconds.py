from typing import Literal

ObsNotificationSignalWindowSeconds = Literal[0, 600]

OBS_NOTIFICATION_SIGNAL_WINDOW_SECONDS_VALUES: set[ObsNotificationSignalWindowSeconds] = {
    0,
    600,
}


def check_obs_notification_signal_window_seconds(value: int) -> ObsNotificationSignalWindowSeconds:
    if value in OBS_NOTIFICATION_SIGNAL_WINDOW_SECONDS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_NOTIFICATION_SIGNAL_WINDOW_SECONDS_VALUES!r}")
