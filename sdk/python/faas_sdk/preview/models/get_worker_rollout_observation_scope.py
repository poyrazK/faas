from typing import Literal

GetWorkerRolloutObservationScope = Literal["default"]

GET_WORKER_ROLLOUT_OBSERVATION_SCOPE_VALUES: set[GetWorkerRolloutObservationScope] = {
    "default",
}


def check_get_worker_rollout_observation_scope(value: str) -> GetWorkerRolloutObservationScope:
    if value in GET_WORKER_ROLLOUT_OBSERVATION_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_WORKER_ROLLOUT_OBSERVATION_SCOPE_VALUES!r}")
