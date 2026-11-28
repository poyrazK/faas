from typing import Literal

ObsServingProbeNotificationFailureCode = Literal[
    "connection", "dns", "http_status", "none", "timeout", "tls", "unknown"
]

OBS_SERVING_PROBE_NOTIFICATION_FAILURE_CODE_VALUES: set[ObsServingProbeNotificationFailureCode] = {
    "connection",
    "dns",
    "http_status",
    "none",
    "timeout",
    "tls",
    "unknown",
}


def check_obs_serving_probe_notification_failure_code(value: str) -> ObsServingProbeNotificationFailureCode:
    if value in OBS_SERVING_PROBE_NOTIFICATION_FAILURE_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_NOTIFICATION_FAILURE_CODE_VALUES!r}"
    )
