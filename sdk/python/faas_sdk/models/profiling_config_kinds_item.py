from typing import Literal

ProfilingConfigKindsItem = Literal["cpu", "heap"]

PROFILING_CONFIG_KINDS_ITEM_VALUES: set[ProfilingConfigKindsItem] = {
    "cpu",
    "heap",
}


def check_profiling_config_kinds_item(value: str) -> ProfilingConfigKindsItem:
    if value in PROFILING_CONFIG_KINDS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILING_CONFIG_KINDS_ITEM_VALUES!r}")
