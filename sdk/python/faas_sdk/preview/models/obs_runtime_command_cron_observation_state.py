from typing import Literal

ObsRuntimeCommandCronObservationState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_COMMAND_CRON_OBSERVATION_STATE_VALUES: set[ObsRuntimeCommandCronObservationState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_command_cron_observation_state(value: str) -> ObsRuntimeCommandCronObservationState:
    if value in OBS_RUNTIME_COMMAND_CRON_OBSERVATION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_OBSERVATION_STATE_VALUES!r}"
    )
