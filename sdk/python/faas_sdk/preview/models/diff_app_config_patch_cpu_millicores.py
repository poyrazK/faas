from typing import Literal

DiffAppConfigPatchCpuMillicores = Literal[250, 500, 1000]

DIFF_APP_CONFIG_PATCH_CPU_MILLICORES_VALUES: set[DiffAppConfigPatchCpuMillicores] = {
    250,
    500,
    1000,
}


def check_diff_app_config_patch_cpu_millicores(value: int) -> DiffAppConfigPatchCpuMillicores:
    if value in DIFF_APP_CONFIG_PATCH_CPU_MILLICORES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DIFF_APP_CONFIG_PATCH_CPU_MILLICORES_VALUES!r}")
