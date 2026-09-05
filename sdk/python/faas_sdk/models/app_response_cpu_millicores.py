from typing import Literal

AppResponseCpuMillicores = Literal[250, 500, 1000]

APP_RESPONSE_CPU_MILLICORES_VALUES: set[AppResponseCpuMillicores] = {
    250,
    500,
    1000,
}


def check_app_response_cpu_millicores(value: int) -> AppResponseCpuMillicores:
    if value in APP_RESPONSE_CPU_MILLICORES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_CPU_MILLICORES_VALUES!r}")
