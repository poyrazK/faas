from typing import Literal

ObsRuntimeCommandCronOutcomesScope = Literal["process_local_counter_observations"]

OBS_RUNTIME_COMMAND_CRON_OUTCOMES_SCOPE_VALUES: set[ObsRuntimeCommandCronOutcomesScope] = {
    "process_local_counter_observations",
}


def check_obs_runtime_command_cron_outcomes_scope(value: str) -> ObsRuntimeCommandCronOutcomesScope:
    if value in OBS_RUNTIME_COMMAND_CRON_OUTCOMES_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_OUTCOMES_SCOPE_VALUES!r}")
