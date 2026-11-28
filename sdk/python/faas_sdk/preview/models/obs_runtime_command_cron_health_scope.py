from typing import Literal

ObsRuntimeCommandCronHealthScope = Literal["scheduler_ownership_scopes"]

OBS_RUNTIME_COMMAND_CRON_HEALTH_SCOPE_VALUES: set[ObsRuntimeCommandCronHealthScope] = {
    "scheduler_ownership_scopes",
}


def check_obs_runtime_command_cron_health_scope(value: str) -> ObsRuntimeCommandCronHealthScope:
    if value in OBS_RUNTIME_COMMAND_CRON_HEALTH_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_HEALTH_SCOPE_VALUES!r}")
