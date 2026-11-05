from typing import Literal

DiffAppConfigPatchEvictionPriority = Literal["batch", "latency", "normal"]

DIFF_APP_CONFIG_PATCH_EVICTION_PRIORITY_VALUES: set[DiffAppConfigPatchEvictionPriority] = {
    "batch",
    "latency",
    "normal",
}


def check_diff_app_config_patch_eviction_priority(value: str) -> DiffAppConfigPatchEvictionPriority:
    if value in DIFF_APP_CONFIG_PATCH_EVICTION_PRIORITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DIFF_APP_CONFIG_PATCH_EVICTION_PRIORITY_VALUES!r}")
