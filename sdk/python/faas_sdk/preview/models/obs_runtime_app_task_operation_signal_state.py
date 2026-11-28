from typing import Literal

ObsRuntimeAppTaskOperationSignalState = Literal[
    "baseline", "conflicting", "increased", "missing", "stale", "unavailable", "unchanged"
]

OBS_RUNTIME_APP_TASK_OPERATION_SIGNAL_STATE_VALUES: set[ObsRuntimeAppTaskOperationSignalState] = {
    "baseline",
    "conflicting",
    "increased",
    "missing",
    "stale",
    "unavailable",
    "unchanged",
}


def check_obs_runtime_app_task_operation_signal_state(value: str) -> ObsRuntimeAppTaskOperationSignalState:
    if value in OBS_RUNTIME_APP_TASK_OPERATION_SIGNAL_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_OPERATION_SIGNAL_STATE_VALUES!r}"
    )
