from typing import Literal

AppSecretResponseLastRuntimeReloadSignal = Literal["failed", "not_attempted", "queued", "sent"]

APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_SIGNAL_VALUES: set[AppSecretResponseLastRuntimeReloadSignal] = {
    "failed",
    "not_attempted",
    "queued",
    "sent",
}


def check_app_secret_response_last_runtime_reload_signal(value: str) -> AppSecretResponseLastRuntimeReloadSignal:
    if value in APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_SIGNAL_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_SECRET_RESPONSE_LAST_RUNTIME_RELOAD_SIGNAL_VALUES!r}"
    )
