from typing import Literal

ObsRuntimeCommandCronTimingSignalState = Literal[
    "baseline", "conflicting", "increased", "missing", "stale", "unavailable", "unchanged"
]

OBS_RUNTIME_COMMAND_CRON_TIMING_SIGNAL_STATE_VALUES: set[ObsRuntimeCommandCronTimingSignalState] = {
    "baseline",
    "conflicting",
    "increased",
    "missing",
    "stale",
    "unavailable",
    "unchanged",
}


def check_obs_runtime_command_cron_timing_signal_state(value: str) -> ObsRuntimeCommandCronTimingSignalState:
    if value in OBS_RUNTIME_COMMAND_CRON_TIMING_SIGNAL_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_TIMING_SIGNAL_STATE_VALUES!r}"
    )
