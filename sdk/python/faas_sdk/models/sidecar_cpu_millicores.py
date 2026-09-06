from typing import Literal

SidecarCpuMillicores = Literal[0, 250, 500, 1000]

SIDECAR_CPU_MILLICORES_VALUES: set[SidecarCpuMillicores] = {
    0,
    250,
    500,
    1000,
}


def check_sidecar_cpu_millicores(value: int) -> SidecarCpuMillicores:
    if value in SIDECAR_CPU_MILLICORES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SIDECAR_CPU_MILLICORES_VALUES!r}")
