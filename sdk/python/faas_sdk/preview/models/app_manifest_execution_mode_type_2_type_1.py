from typing import Literal

AppManifestExecutionModeType2Type1 = Literal["job", "request", "service", "worker"]

APP_MANIFEST_EXECUTION_MODE_TYPE_2_TYPE_1_VALUES: set[AppManifestExecutionModeType2Type1] = {
    "job",
    "request",
    "service",
    "worker",
}


def check_app_manifest_execution_mode_type_2_type_1(value: str) -> AppManifestExecutionModeType2Type1:
    if value in APP_MANIFEST_EXECUTION_MODE_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_MANIFEST_EXECUTION_MODE_TYPE_2_TYPE_1_VALUES!r}")
