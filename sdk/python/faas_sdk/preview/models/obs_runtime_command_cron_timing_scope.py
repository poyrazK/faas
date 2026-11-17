from typing import Literal

ObsRuntimeCommandCronTimingScope = Literal["process_local_counter_observations"]

OBS_RUNTIME_COMMAND_CRON_TIMING_SCOPE_VALUES: set[ObsRuntimeCommandCronTimingScope] = {
    "process_local_counter_observations",
}


def check_obs_runtime_command_cron_timing_scope(value: str) -> ObsRuntimeCommandCronTimingScope:
    if value in OBS_RUNTIME_COMMAND_CRON_TIMING_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_TIMING_SCOPE_VALUES!r}")
