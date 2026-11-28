from typing import Literal

ObsRuntimeReadinessReasonCountReason = Literal[
    "credentials", "database", "draining", "grpc", "other", "process", "ready", "stale", "storage", "vmmd"
]

OBS_RUNTIME_READINESS_REASON_COUNT_REASON_VALUES: set[ObsRuntimeReadinessReasonCountReason] = {
    "credentials",
    "database",
    "draining",
    "grpc",
    "other",
    "process",
    "ready",
    "stale",
    "storage",
    "vmmd",
}


def check_obs_runtime_readiness_reason_count_reason(value: str) -> ObsRuntimeReadinessReasonCountReason:
    if value in OBS_RUNTIME_READINESS_REASON_COUNT_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_READINESS_REASON_COUNT_REASON_VALUES!r}")
