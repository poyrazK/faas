from typing import Literal

DiffAppConfigPatchExecutionMode = Literal["job", "request", "service", "worker"]

DIFF_APP_CONFIG_PATCH_EXECUTION_MODE_VALUES: set[DiffAppConfigPatchExecutionMode] = {
    "job",
    "request",
    "service",
    "worker",
}


def check_diff_app_config_patch_execution_mode(value: str) -> DiffAppConfigPatchExecutionMode:
    if value in DIFF_APP_CONFIG_PATCH_EXECUTION_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DIFF_APP_CONFIG_PATCH_EXECUTION_MODE_VALUES!r}")
