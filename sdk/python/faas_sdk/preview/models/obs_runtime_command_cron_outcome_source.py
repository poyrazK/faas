from typing import Literal

ObsRuntimeCommandCronOutcomeSource = Literal["admission", "decision", "sweep"]

OBS_RUNTIME_COMMAND_CRON_OUTCOME_SOURCE_VALUES: set[ObsRuntimeCommandCronOutcomeSource] = {
    "admission",
    "decision",
    "sweep",
}


def check_obs_runtime_command_cron_outcome_source(value: str) -> ObsRuntimeCommandCronOutcomeSource:
    if value in OBS_RUNTIME_COMMAND_CRON_OUTCOME_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_OUTCOME_SOURCE_VALUES!r}")
