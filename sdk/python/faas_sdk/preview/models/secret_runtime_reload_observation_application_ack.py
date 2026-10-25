from typing import Literal

SecretRuntimeReloadObservationApplicationAck = Literal["applied", "failed"]

SECRET_RUNTIME_RELOAD_OBSERVATION_APPLICATION_ACK_VALUES: set[SecretRuntimeReloadObservationApplicationAck] = {
    "applied",
    "failed",
}


def check_secret_runtime_reload_observation_application_ack(value: str) -> SecretRuntimeReloadObservationApplicationAck:
    if value in SECRET_RUNTIME_RELOAD_OBSERVATION_APPLICATION_ACK_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SECRET_RUNTIME_RELOAD_OBSERVATION_APPLICATION_ACK_VALUES!r}"
    )
