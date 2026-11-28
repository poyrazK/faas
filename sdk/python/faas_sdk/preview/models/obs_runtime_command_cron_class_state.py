from typing import Literal

ObsRuntimeCommandCronClassState = Literal["conflicting", "empty", "missing", "nonempty", "stale", "unavailable"]

OBS_RUNTIME_COMMAND_CRON_CLASS_STATE_VALUES: set[ObsRuntimeCommandCronClassState] = {
    "conflicting",
    "empty",
    "missing",
    "nonempty",
    "stale",
    "unavailable",
}


def check_obs_runtime_command_cron_class_state(value: str) -> ObsRuntimeCommandCronClassState:
    if value in OBS_RUNTIME_COMMAND_CRON_CLASS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_CLASS_STATE_VALUES!r}")
