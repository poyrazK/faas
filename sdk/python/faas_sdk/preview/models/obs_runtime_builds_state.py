from typing import Literal

ObsRuntimeBuildsState = Literal["incomplete", "matching", "mismatched", "mixed", "unknown"]

OBS_RUNTIME_BUILDS_STATE_VALUES: set[ObsRuntimeBuildsState] = {
    "incomplete",
    "matching",
    "mismatched",
    "mixed",
    "unknown",
}


def check_obs_runtime_builds_state(value: str) -> ObsRuntimeBuildsState:
    if value in OBS_RUNTIME_BUILDS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_BUILDS_STATE_VALUES!r}")
