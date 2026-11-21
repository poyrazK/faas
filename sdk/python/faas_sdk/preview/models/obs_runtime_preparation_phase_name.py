from typing import Literal

ObsRuntimePreparationPhaseName = Literal["layer_published", "preparing", "scan_complete"]

OBS_RUNTIME_PREPARATION_PHASE_NAME_VALUES: set[ObsRuntimePreparationPhaseName] = {
    "layer_published",
    "preparing",
    "scan_complete",
}


def check_obs_runtime_preparation_phase_name(value: str) -> ObsRuntimePreparationPhaseName:
    if value in OBS_RUNTIME_PREPARATION_PHASE_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PREPARATION_PHASE_NAME_VALUES!r}")
