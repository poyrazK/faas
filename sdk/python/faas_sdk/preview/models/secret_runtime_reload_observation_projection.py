from typing import Literal

SecretRuntimeReloadObservationProjection = Literal["failed", "unchanged", "updated"]

SECRET_RUNTIME_RELOAD_OBSERVATION_PROJECTION_VALUES: set[SecretRuntimeReloadObservationProjection] = {
    "failed",
    "unchanged",
    "updated",
}


def check_secret_runtime_reload_observation_projection(value: str) -> SecretRuntimeReloadObservationProjection:
    if value in SECRET_RUNTIME_RELOAD_OBSERVATION_PROJECTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SECRET_RUNTIME_RELOAD_OBSERVATION_PROJECTION_VALUES!r}"
    )
