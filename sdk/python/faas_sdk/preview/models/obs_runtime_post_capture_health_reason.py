from typing import Literal

ObsRuntimePostCaptureHealthReason = Literal[
    "incomplete_post_capture_evidence", "post_capture_evidence_observed", "post_capture_evidence_unavailable"
]

OBS_RUNTIME_POST_CAPTURE_HEALTH_REASON_VALUES: set[ObsRuntimePostCaptureHealthReason] = {
    "incomplete_post_capture_evidence",
    "post_capture_evidence_observed",
    "post_capture_evidence_unavailable",
}


def check_obs_runtime_post_capture_health_reason(value: str) -> ObsRuntimePostCaptureHealthReason:
    if value in OBS_RUNTIME_POST_CAPTURE_HEALTH_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_POST_CAPTURE_HEALTH_REASON_VALUES!r}")
