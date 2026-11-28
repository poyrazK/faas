from typing import Literal

ComposeResourcesCpuMillicores = Literal[250, 500, 1000]

COMPOSE_RESOURCES_CPU_MILLICORES_VALUES: set[ComposeResourcesCpuMillicores] = {
    250,
    500,
    1000,
}


def check_compose_resources_cpu_millicores(value: int) -> ComposeResourcesCpuMillicores:
    if value in COMPOSE_RESOURCES_CPU_MILLICORES_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {COMPOSE_RESOURCES_CPU_MILLICORES_VALUES!r}")
