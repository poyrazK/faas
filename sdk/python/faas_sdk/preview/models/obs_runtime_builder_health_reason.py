from typing import Literal

ObsRuntimeBuilderHealthReason = Literal[
    "builder_evidence_unavailable", "incomplete_builder_evidence", "worker_progress_overdue"
]

OBS_RUNTIME_BUILDER_HEALTH_REASON_VALUES: set[ObsRuntimeBuilderHealthReason] = {
    "builder_evidence_unavailable",
    "incomplete_builder_evidence",
    "worker_progress_overdue",
}


def check_obs_runtime_builder_health_reason(value: str) -> ObsRuntimeBuilderHealthReason:
    if value in OBS_RUNTIME_BUILDER_HEALTH_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDER_HEALTH_REASON_VALUES!r}")
