from typing import Literal

ObsRuntimeCommandCronTimingName = Literal[
    "le_120_seconds",
    "le_15_seconds",
    "le_1800_seconds",
    "le_1_seconds",
    "le_300_seconds",
    "le_30_seconds",
    "le_3600_seconds",
    "le_5_seconds",
    "le_600_seconds",
    "le_60_seconds",
    "le_inf",
    "timed",
    "unknown_timestamps",
]

OBS_RUNTIME_COMMAND_CRON_TIMING_NAME_VALUES: set[ObsRuntimeCommandCronTimingName] = {
    "le_120_seconds",
    "le_15_seconds",
    "le_1800_seconds",
    "le_1_seconds",
    "le_300_seconds",
    "le_30_seconds",
    "le_3600_seconds",
    "le_5_seconds",
    "le_600_seconds",
    "le_60_seconds",
    "le_inf",
    "timed",
    "unknown_timestamps",
}


def check_obs_runtime_command_cron_timing_name(value: str) -> ObsRuntimeCommandCronTimingName:
    if value in OBS_RUNTIME_COMMAND_CRON_TIMING_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_TIMING_NAME_VALUES!r}")
