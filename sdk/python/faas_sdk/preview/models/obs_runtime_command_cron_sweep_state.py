from typing import Literal

ObsRuntimeCommandCronSweepState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_COMMAND_CRON_SWEEP_STATE_VALUES: set[ObsRuntimeCommandCronSweepState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_command_cron_sweep_state(value: str) -> ObsRuntimeCommandCronSweepState:
    if value in OBS_RUNTIME_COMMAND_CRON_SWEEP_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_SWEEP_STATE_VALUES!r}")
