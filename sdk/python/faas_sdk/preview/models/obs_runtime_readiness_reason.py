from typing import Literal

ObsRuntimeReadinessReason = Literal["fresh_not_ready", "incomplete_readiness_evidence", "readiness_unavailable"]

OBS_RUNTIME_READINESS_REASON_VALUES: set[ObsRuntimeReadinessReason] = {
    "fresh_not_ready",
    "incomplete_readiness_evidence",
    "readiness_unavailable",
}


def check_obs_runtime_readiness_reason(value: str) -> ObsRuntimeReadinessReason:
    if value in OBS_RUNTIME_READINESS_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_READINESS_REASON_VALUES!r}")
