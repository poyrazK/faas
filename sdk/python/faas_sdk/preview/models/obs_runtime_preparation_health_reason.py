from typing import Literal

ObsRuntimePreparationHealthReason = Literal[
    "incomplete_preparation_evidence", "pending_preparations_observed", "preparation_evidence_unavailable"
]

OBS_RUNTIME_PREPARATION_HEALTH_REASON_VALUES: set[ObsRuntimePreparationHealthReason] = {
    "incomplete_preparation_evidence",
    "pending_preparations_observed",
    "preparation_evidence_unavailable",
}


def check_obs_runtime_preparation_health_reason(value: str) -> ObsRuntimePreparationHealthReason:
    if value in OBS_RUNTIME_PREPARATION_HEALTH_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PREPARATION_HEALTH_REASON_VALUES!r}")
