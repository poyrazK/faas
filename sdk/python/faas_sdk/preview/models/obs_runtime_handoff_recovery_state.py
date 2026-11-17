from typing import Literal

ObsRuntimeHandoffRecoveryState = Literal[
    "conflicting", "missing", "not_started", "stale", "stopping", "unavailable", "waiting", "working"
]

OBS_RUNTIME_HANDOFF_RECOVERY_STATE_VALUES: set[ObsRuntimeHandoffRecoveryState] = {
    "conflicting",
    "missing",
    "not_started",
    "stale",
    "stopping",
    "unavailable",
    "waiting",
    "working",
}


def check_obs_runtime_handoff_recovery_state(value: str) -> ObsRuntimeHandoffRecoveryState:
    if value in OBS_RUNTIME_HANDOFF_RECOVERY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HANDOFF_RECOVERY_STATE_VALUES!r}")
