from typing import Literal

ObsServingProbeNotificationEvidenceReason = Literal[
    "no_submission_yet", "source_unavailable", "telemetry_invalid", "telemetry_missing"
]

OBS_SERVING_PROBE_NOTIFICATION_EVIDENCE_REASON_VALUES: set[ObsServingProbeNotificationEvidenceReason] = {
    "no_submission_yet",
    "source_unavailable",
    "telemetry_invalid",
    "telemetry_missing",
}


def check_obs_serving_probe_notification_evidence_reason(value: str) -> ObsServingProbeNotificationEvidenceReason:
    if value in OBS_SERVING_PROBE_NOTIFICATION_EVIDENCE_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_NOTIFICATION_EVIDENCE_REASON_VALUES!r}"
    )
