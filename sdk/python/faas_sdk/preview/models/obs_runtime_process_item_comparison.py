from typing import Literal

ObsRuntimeProcessItemComparison = Literal[
    "baseline", "changed", "new_target", "repeated_sample", "unavailable", "unchanged"
]

OBS_RUNTIME_PROCESS_ITEM_COMPARISON_VALUES: set[ObsRuntimeProcessItemComparison] = {
    "baseline",
    "changed",
    "new_target",
    "repeated_sample",
    "unavailable",
    "unchanged",
}


def check_obs_runtime_process_item_comparison(value: str) -> ObsRuntimeProcessItemComparison:
    if value in OBS_RUNTIME_PROCESS_ITEM_COMPARISON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PROCESS_ITEM_COMPARISON_VALUES!r}")
