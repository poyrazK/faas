from typing import Literal

UpdateAppRequestCpuMillicoresType1 = Literal[250, 500, 1000]

UPDATE_APP_REQUEST_CPU_MILLICORES_TYPE_1_VALUES: set[UpdateAppRequestCpuMillicoresType1] = {
    250,
    500,
    1000,
}


def check_update_app_request_cpu_millicores_type_1(value: int) -> UpdateAppRequestCpuMillicoresType1:
    if value in UPDATE_APP_REQUEST_CPU_MILLICORES_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_CPU_MILLICORES_TYPE_1_VALUES!r}")
