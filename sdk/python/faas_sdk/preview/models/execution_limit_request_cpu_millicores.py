from typing import Literal

ExecutionLimitRequestCpuMillicores = Literal[0, 250, 500, 1000]

EXECUTION_LIMIT_REQUEST_CPU_MILLICORES_VALUES: set[ExecutionLimitRequestCpuMillicores] = {
    0,
    250,
    500,
    1000,
}


def check_execution_limit_request_cpu_millicores(value: int) -> ExecutionLimitRequestCpuMillicores:
    if value in EXECUTION_LIMIT_REQUEST_CPU_MILLICORES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EXECUTION_LIMIT_REQUEST_CPU_MILLICORES_VALUES!r}")
