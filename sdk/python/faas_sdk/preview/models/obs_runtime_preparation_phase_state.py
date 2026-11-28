from typing import Literal

ObsRuntimePreparationPhaseState = Literal["conflicting", "empty", "missing", "stale", "unavailable", "waiting"]

OBS_RUNTIME_PREPARATION_PHASE_STATE_VALUES: set[ObsRuntimePreparationPhaseState] = {
    "conflicting",
    "empty",
    "missing",
    "stale",
    "unavailable",
    "waiting",
}


def check_obs_runtime_preparation_phase_state(value: str) -> ObsRuntimePreparationPhaseState:
    if value in OBS_RUNTIME_PREPARATION_PHASE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PREPARATION_PHASE_STATE_VALUES!r}")
