from typing import Literal

ObsServingProbeNotificationReason = Literal[
    "metadata_inconsistent",
    "query_failed",
    "recording_missing",
    "recording_not_current",
    "recording_unknown",
    "source_unavailable",
]

OBS_SERVING_PROBE_NOTIFICATION_REASON_VALUES: set[ObsServingProbeNotificationReason] = {
    "metadata_inconsistent",
    "query_failed",
    "recording_missing",
    "recording_not_current",
    "recording_unknown",
    "source_unavailable",
}


def check_obs_serving_probe_notification_reason(value: str) -> ObsServingProbeNotificationReason:
    if value in OBS_SERVING_PROBE_NOTIFICATION_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_NOTIFICATION_REASON_VALUES!r}")
