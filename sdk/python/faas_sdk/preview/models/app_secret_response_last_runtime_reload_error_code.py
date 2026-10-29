from typing import Literal

AppSecretResponseLastRuntimeReloadErrorCode = Literal["projection_failed", "signal_failed"]

APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_ERROR_CODE_VALUES: set[AppSecretResponseLastRuntimeReloadErrorCode] = {
    "projection_failed",
    "signal_failed",
}


def check_app_secret_response_last_runtime_reload_error_code(value: str) -> AppSecretResponseLastRuntimeReloadErrorCode:
    if value in APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_ERROR_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_ERROR_CODE_VALUES!r}"
    )
