from typing import Literal

AppManifestPause = Literal["between_requests", "never", "when_idle"]

APP_MANIFEST_PAUSE_VALUES: set[AppManifestPause] = {
    "between_requests",
    "never",
    "when_idle",
}


def check_app_manifest_pause(value: str) -> AppManifestPause:
    if value in APP_MANIFEST_PAUSE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_MANIFEST_PAUSE_VALUES!r}")
