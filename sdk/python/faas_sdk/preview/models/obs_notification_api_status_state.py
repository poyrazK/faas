from typing import Literal

ObsNotificationAPIStatusState = Literal["disabled", "healthy", "unavailable"]

OBS_NOTIFICATION_API_STATUS_STATE_VALUES: set[ObsNotificationAPIStatusState] = {
    "disabled",
    "healthy",
    "unavailable",
}


def check_obs_notification_api_status_state(value: str) -> ObsNotificationAPIStatusState:
    if value in OBS_NOTIFICATION_API_STATUS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_NOTIFICATION_API_STATUS_STATE_VALUES!r}")
