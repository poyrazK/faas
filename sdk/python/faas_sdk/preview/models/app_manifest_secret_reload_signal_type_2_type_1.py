from typing import Literal

AppManifestSecretReloadSignalType2Type1 = Literal["SIGHUP", "SIGUSR1", "SIGUSR2"]

APP_MANIFEST_SECRET_RELOAD_SIGNAL_TYPE_2_TYPE_1_VALUES: set[AppManifestSecretReloadSignalType2Type1] = {
    "SIGHUP",
    "SIGUSR1",
    "SIGUSR2",
}


def check_app_manifest_secret_reload_signal_type_2_type_1(value: str) -> AppManifestSecretReloadSignalType2Type1:
    if value in APP_MANIFEST_SECRET_RELOAD_SIGNAL_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_MANIFEST_SECRET_RELOAD_SIGNAL_TYPE_2_TYPE_1_VALUES!r}"
    )
