from typing import Literal

AppManifestSecretReloadSignalType3Type1 = Literal["SIGHUP", "SIGUSR1", "SIGUSR2"]

APP_MANIFEST_SECRET_RELOAD_SIGNAL_TYPE_3_TYPE_1_VALUES: set[AppManifestSecretReloadSignalType3Type1] = {
    "SIGHUP",
    "SIGUSR1",
    "SIGUSR2",
}


def check_app_manifest_secret_reload_signal_type_3_type_1(value: str) -> AppManifestSecretReloadSignalType3Type1:
    if value in APP_MANIFEST_SECRET_RELOAD_SIGNAL_TYPE_3_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_MANIFEST_SECRET_RELOAD_SIGNAL_TYPE_3_TYPE_1_VALUES!r}"
    )
