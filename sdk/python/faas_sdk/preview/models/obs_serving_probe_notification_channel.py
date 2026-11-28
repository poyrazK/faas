from typing import Literal

ObsServingProbeNotificationChannel = Literal["heartbeat", "webhook"]

OBS_SERVING_PROBE_NOTIFICATION_CHANNEL_VALUES: set[ObsServingProbeNotificationChannel] = {
    "heartbeat",
    "webhook",
}


def check_obs_serving_probe_notification_channel(value: str) -> ObsServingProbeNotificationChannel:
    if value in OBS_SERVING_PROBE_NOTIFICATION_CHANNEL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_NOTIFICATION_CHANNEL_VALUES!r}")
