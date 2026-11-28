from typing import Literal

ObsRuntimeSnapshotPrimeHealthReason = Literal[
    "incomplete_snapshot_prime_evidence", "snapshot_attempts_observed", "snapshot_prime_evidence_unavailable"
]

OBS_RUNTIME_SNAPSHOT_PRIME_HEALTH_REASON_VALUES: set[ObsRuntimeSnapshotPrimeHealthReason] = {
    "incomplete_snapshot_prime_evidence",
    "snapshot_attempts_observed",
    "snapshot_prime_evidence_unavailable",
}


def check_obs_runtime_snapshot_prime_health_reason(value: str) -> ObsRuntimeSnapshotPrimeHealthReason:
    if value in OBS_RUNTIME_SNAPSHOT_PRIME_HEALTH_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_SNAPSHOT_PRIME_HEALTH_REASON_VALUES!r}")
