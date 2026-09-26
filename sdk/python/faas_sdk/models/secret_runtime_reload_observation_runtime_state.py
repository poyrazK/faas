from typing import Literal

SecretRuntimeReloadObservationRuntimeState = Literal[
    "cold_booting", "draining", "migrating", "running", "snapshotting", "waking", "warm"
]

SECRET_RUNTIME_RELOAD_OBSERVATION_RUNTIME_STATE_VALUES: set[SecretRuntimeReloadObservationRuntimeState] = {
    "cold_booting",
    "draining",
    "migrating",
    "running",
    "snapshotting",
    "waking",
    "warm",
}


def check_secret_runtime_reload_observation_runtime_state(value: str) -> SecretRuntimeReloadObservationRuntimeState:
    if value in SECRET_RUNTIME_RELOAD_OBSERVATION_RUNTIME_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SECRET_RUNTIME_RELOAD_OBSERVATION_RUNTIME_STATE_VALUES!r}"
    )
