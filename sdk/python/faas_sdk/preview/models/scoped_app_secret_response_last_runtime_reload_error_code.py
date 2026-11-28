from typing import Literal

ScopedAppSecretResponseLastRuntimeReloadErrorCode = Literal["projection_failed", "signal_failed"]

SCOPED_APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_ERROR_CODE_VALUES: set[
    ScopedAppSecretResponseLastRuntimeReloadErrorCode
] = {
    "projection_failed",
    "signal_failed",
}


def check_scoped_app_secret_response_last_runtime_reload_error_code(
    value: str,
) -> ScopedAppSecretResponseLastRuntimeReloadErrorCode:
    if value in SCOPED_APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_ERROR_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SCOPED_APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_ERROR_CODE_VALUES!r}"
    )
