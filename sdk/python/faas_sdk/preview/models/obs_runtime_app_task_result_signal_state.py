from typing import Literal

ObsRuntimeAppTaskResultSignalState = Literal[
    "baseline", "conflicting", "increased", "missing", "stale", "unavailable", "unchanged"
]

OBS_RUNTIME_APP_TASK_RESULT_SIGNAL_STATE_VALUES: set[ObsRuntimeAppTaskResultSignalState] = {
    "baseline",
    "conflicting",
    "increased",
    "missing",
    "stale",
    "unavailable",
    "unchanged",
}


def check_obs_runtime_app_task_result_signal_state(value: str) -> ObsRuntimeAppTaskResultSignalState:
    if value in OBS_RUNTIME_APP_TASK_RESULT_SIGNAL_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_RESULT_SIGNAL_STATE_VALUES!r}")
