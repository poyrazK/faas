from typing import Literal

WorkerRolloutObservationBasis = Literal["resident_generation_snapshot"]

WORKER_ROLLOUT_OBSERVATION_BASIS_VALUES: set[WorkerRolloutObservationBasis] = {
    "resident_generation_snapshot",
}


def check_worker_rollout_observation_basis(value: str) -> WorkerRolloutObservationBasis:
    if value in WORKER_ROLLOUT_OBSERVATION_BASIS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKER_ROLLOUT_OBSERVATION_BASIS_VALUES!r}")
