from typing import Literal

ObsRuntimeCommandCronHealthState = Literal["empty", "incomplete", "nonempty", "unknown"]

OBS_RUNTIME_COMMAND_CRON_HEALTH_STATE_VALUES: set[ObsRuntimeCommandCronHealthState] = {
    "empty",
    "incomplete",
    "nonempty",
    "unknown",
}


def check_obs_runtime_command_cron_health_state(value: str) -> ObsRuntimeCommandCronHealthState:
    if value in OBS_RUNTIME_COMMAND_CRON_HEALTH_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_HEALTH_STATE_VALUES!r}")
