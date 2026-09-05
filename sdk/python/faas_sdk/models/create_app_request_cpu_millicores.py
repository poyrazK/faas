from typing import Literal

CreateAppRequestCpuMillicores = Literal[250, 500, 1000]

CREATE_APP_REQUEST_CPU_MILLICORES_VALUES: set[CreateAppRequestCpuMillicores] = {
    250,
    500,
    1000,
}


def check_create_app_request_cpu_millicores(value: int) -> CreateAppRequestCpuMillicores:
    if value in CREATE_APP_REQUEST_CPU_MILLICORES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_CPU_MILLICORES_VALUES!r}")
