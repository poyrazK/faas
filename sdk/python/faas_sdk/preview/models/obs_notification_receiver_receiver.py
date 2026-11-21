from typing import Literal

ObsNotificationReceiverReceiver = Literal["faas-heartbeat", "faas-page", "faas-warn"]

OBS_NOTIFICATION_RECEIVER_RECEIVER_VALUES: set[ObsNotificationReceiverReceiver] = {
    "faas-heartbeat",
    "faas-page",
    "faas-warn",
}


def check_obs_notification_receiver_receiver(value: str) -> ObsNotificationReceiverReceiver:
    if value in OBS_NOTIFICATION_RECEIVER_RECEIVER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_NOTIFICATION_RECEIVER_RECEIVER_VALUES!r}")
