from typing import Literal

ObsRuntimeCommandCronItemState = Literal["available", "conflicting", "missing", "stale", "unavailable"]

OBS_RUNTIME_COMMAND_CRON_ITEM_STATE_VALUES: set[ObsRuntimeCommandCronItemState] = {
    "available",
    "conflicting",
    "missing",
    "stale",
    "unavailable",
}


def check_obs_runtime_command_cron_item_state(value: str) -> ObsRuntimeCommandCronItemState:
    if value in OBS_RUNTIME_COMMAND_CRON_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_ITEM_STATE_VALUES!r}")
