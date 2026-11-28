from typing import Literal

ObsRuntimeBuildRecoveryHealthReason = Literal[
    "build_recovery_evidence_observed", "build_recovery_evidence_unavailable", "incomplete_build_recovery_evidence"
]

OBS_RUNTIME_BUILD_RECOVERY_HEALTH_REASON_VALUES: set[ObsRuntimeBuildRecoveryHealthReason] = {
    "build_recovery_evidence_observed",
    "build_recovery_evidence_unavailable",
    "incomplete_build_recovery_evidence",
}


def check_obs_runtime_build_recovery_health_reason(value: str) -> ObsRuntimeBuildRecoveryHealthReason:
    if value in OBS_RUNTIME_BUILD_RECOVERY_HEALTH_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILD_RECOVERY_HEALTH_REASON_VALUES!r}")
