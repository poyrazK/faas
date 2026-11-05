from typing import Literal

AppConfiguredResourcesCpuMillicores = Literal[250, 500, 1000]

APP_CONFIGURED_RESOURCES_CPU_MILLICORES_VALUES: set[AppConfiguredResourcesCpuMillicores] = {
    250,
    500,
    1000,
}


def check_app_configured_resources_cpu_millicores(value: int) -> AppConfiguredResourcesCpuMillicores:
    if value in APP_CONFIGURED_RESOURCES_CPU_MILLICORES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_CONFIGURED_RESOURCES_CPU_MILLICORES_VALUES!r}")
