from typing import Literal

SecretRuntimeReloadObservationErrorCode = Literal["projection_failed", "signal_failed"]

SECRET_RUNTIME_RELOAD_OBSERVATION_ERROR_CODE_VALUES: set[SecretRuntimeReloadObservationErrorCode] = {
    "projection_failed",
    "signal_failed",
}


def check_secret_runtime_reload_observation_error_code(value: str) -> SecretRuntimeReloadObservationErrorCode:
    if value in SECRET_RUNTIME_RELOAD_OBSERVATION_ERROR_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SECRET_RUNTIME_RELOAD_OBSERVATION_ERROR_CODE_VALUES!r}"
    )
