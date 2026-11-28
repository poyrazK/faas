from typing import Literal

ObsRuntimeProcessState = Literal["baseline", "changed", "incomplete", "unchanged", "unknown"]

OBS_RUNTIME_PROCESS_STATE_VALUES: set[ObsRuntimeProcessState] = {
    "baseline",
    "changed",
    "incomplete",
    "unchanged",
    "unknown",
}


def check_obs_runtime_process_state(value: str) -> ObsRuntimeProcessState:
    if value in OBS_RUNTIME_PROCESS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PROCESS_STATE_VALUES!r}")
