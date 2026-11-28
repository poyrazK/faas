from typing import Literal

ObsRuntimeCommandCronOutcomeSignalState = Literal[
    "baseline", "conflicting", "increased", "missing", "stale", "unavailable", "unchanged"
]

OBS_RUNTIME_COMMAND_CRON_OUTCOME_SIGNAL_STATE_VALUES: set[ObsRuntimeCommandCronOutcomeSignalState] = {
    "baseline",
    "conflicting",
    "increased",
    "missing",
    "stale",
    "unavailable",
    "unchanged",
}


def check_obs_runtime_command_cron_outcome_signal_state(value: str) -> ObsRuntimeCommandCronOutcomeSignalState:
    if value in OBS_RUNTIME_COMMAND_CRON_OUTCOME_SIGNAL_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_OUTCOME_SIGNAL_STATE_VALUES!r}"
    )
