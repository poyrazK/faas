from typing import Literal

SecretRuntimeReloadObservationSignal = Literal["failed", "not_attempted", "queued", "sent"]

SECRET_RUNTIME_RELOAD_OBSERVATION_SIGNAL_VALUES: set[SecretRuntimeReloadObservationSignal] = {
    "failed",
    "not_attempted",
    "queued",
    "sent",
}


def check_secret_runtime_reload_observation_signal(value: str) -> SecretRuntimeReloadObservationSignal:
    if value in SECRET_RUNTIME_RELOAD_OBSERVATION_SIGNAL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SECRET_RUNTIME_RELOAD_OBSERVATION_SIGNAL_VALUES!r}")
