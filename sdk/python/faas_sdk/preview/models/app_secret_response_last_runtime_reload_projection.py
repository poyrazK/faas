from typing import Literal

AppSecretResponseLastRuntimeReloadProjection = Literal["failed", "unchanged", "updated"]

APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_PROJECTION_VALUES: set[AppSecretResponseLastRuntimeReloadProjection] = {
    "failed",
    "unchanged",
    "updated",
}


def check_app_secret_response_last_runtime_reload_projection(
    value: str,
) -> AppSecretResponseLastRuntimeReloadProjection:
    if value in APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_PROJECTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_PROJECTION_VALUES!r}"
    )
