from typing import Literal

ObsServingProbeNotificationHealthState = Literal["available", "not_applicable", "partial", "unknown"]

OBS_SERVING_PROBE_NOTIFICATION_HEALTH_STATE_VALUES: set[ObsServingProbeNotificationHealthState] = {
    "available",
    "not_applicable",
    "partial",
    "unknown",
}


def check_obs_serving_probe_notification_health_state(value: str) -> ObsServingProbeNotificationHealthState:
    if value in OBS_SERVING_PROBE_NOTIFICATION_HEALTH_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_NOTIFICATION_HEALTH_STATE_VALUES!r}"
    )
