from typing import Literal

ObsRuntimeCommandCronOutcomesState = Literal["baseline", "incomplete", "increased", "unchanged", "unknown"]

OBS_RUNTIME_COMMAND_CRON_OUTCOMES_STATE_VALUES: set[ObsRuntimeCommandCronOutcomesState] = {
    "baseline",
    "incomplete",
    "increased",
    "unchanged",
    "unknown",
}


def check_obs_runtime_command_cron_outcomes_state(value: str) -> ObsRuntimeCommandCronOutcomesState:
    if value in OBS_RUNTIME_COMMAND_CRON_OUTCOMES_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_OUTCOMES_STATE_VALUES!r}")
