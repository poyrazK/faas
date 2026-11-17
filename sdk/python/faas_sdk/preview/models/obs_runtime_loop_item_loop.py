from typing import Literal

ObsRuntimeLoopItemLoop = Literal["main", "runtime", "sweep"]

OBS_RUNTIME_LOOP_ITEM_LOOP_VALUES: set[ObsRuntimeLoopItemLoop] = {
    "main",
    "runtime",
    "sweep",
}


def check_obs_runtime_loop_item_loop(value: str) -> ObsRuntimeLoopItemLoop:
    if value in OBS_RUNTIME_LOOP_ITEM_LOOP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_LOOP_ITEM_LOOP_VALUES!r}")
