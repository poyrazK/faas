from typing import Literal

ObsRuntimeHandoffBacklogState = Literal["conflicting", "empty", "missing", "stale", "unavailable", "waiting"]

OBS_RUNTIME_HANDOFF_BACKLOG_STATE_VALUES: set[ObsRuntimeHandoffBacklogState] = {
    "conflicting",
    "empty",
    "missing",
    "stale",
    "unavailable",
    "waiting",
}


def check_obs_runtime_handoff_backlog_state(value: str) -> ObsRuntimeHandoffBacklogState:
    if value in OBS_RUNTIME_HANDOFF_BACKLOG_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HANDOFF_BACKLOG_STATE_VALUES!r}")
