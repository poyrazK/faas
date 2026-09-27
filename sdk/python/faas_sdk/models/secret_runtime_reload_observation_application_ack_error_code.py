from typing import Literal

SecretRuntimeReloadObservationApplicationAckErrorCode = Literal["application_reload_failed"]

SECRET_RUNTIME_RELOAD_OBSERVATION_APPLICATION_ACK_ERROR_CODE_VALUES: set[
    SecretRuntimeReloadObservationApplicationAckErrorCode
] = {
    "application_reload_failed",
}


def check_secret_runtime_reload_observation_application_ack_error_code(
    value: str,
) -> SecretRuntimeReloadObservationApplicationAckErrorCode:
    if value in SECRET_RUNTIME_RELOAD_OBSERVATION_APPLICATION_ACK_ERROR_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SECRET_RUNTIME_RELOAD_OBSERVATION_APPLICATION_ACK_ERROR_CODE_VALUES!r}"
    )
