from typing import Literal

ScopedAppSecretResponseLastRuntimeReloadProjection = Literal["failed", "unchanged", "updated"]

SCOPED_APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_PROJECTION_VALUES: set[
    ScopedAppSecretResponseLastRuntimeReloadProjection
] = {
    "failed",
    "unchanged",
    "updated",
}


def check_scoped_app_secret_response_last_runtime_reload_projection(
    value: str,
) -> ScopedAppSecretResponseLastRuntimeReloadProjection:
    if value in SCOPED_APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_PROJECTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SCOPED_APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_PROJECTION_VALUES!r}"
    )
