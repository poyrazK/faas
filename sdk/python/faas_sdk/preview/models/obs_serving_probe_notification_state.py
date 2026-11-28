from typing import Literal

ObsServingProbeNotificationState = Literal["disabled", "last_submission_failed", "last_submission_succeeded", "unknown"]

OBS_SERVING_PROBE_NOTIFICATION_STATE_VALUES: set[ObsServingProbeNotificationState] = {
    "disabled",
    "last_submission_failed",
    "last_submission_succeeded",
    "unknown",
}


def check_obs_serving_probe_notification_state(value: str) -> ObsServingProbeNotificationState:
    if value in OBS_SERVING_PROBE_NOTIFICATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_NOTIFICATION_STATE_VALUES!r}")
