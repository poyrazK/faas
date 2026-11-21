from typing import Literal

ObsRuntimeCommandCronTimingState = Literal["baseline", "incomplete", "increased", "unchanged", "unknown"]

OBS_RUNTIME_COMMAND_CRON_TIMING_STATE_VALUES: set[ObsRuntimeCommandCronTimingState] = {
    "baseline",
    "incomplete",
    "increased",
    "unchanged",
    "unknown",
}


def check_obs_runtime_command_cron_timing_state(value: str) -> ObsRuntimeCommandCronTimingState:
    if value in OBS_RUNTIME_COMMAND_CRON_TIMING_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_TIMING_STATE_VALUES!r}")
