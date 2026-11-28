from typing import Literal

ObsRuntimeAppTaskResultsState = Literal["baseline", "incomplete", "increased", "unchanged", "unknown"]

OBS_RUNTIME_APP_TASK_RESULTS_STATE_VALUES: set[ObsRuntimeAppTaskResultsState] = {
    "baseline",
    "incomplete",
    "increased",
    "unchanged",
    "unknown",
}


def check_obs_runtime_app_task_results_state(value: str) -> ObsRuntimeAppTaskResultsState:
    if value in OBS_RUNTIME_APP_TASK_RESULTS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_RESULTS_STATE_VALUES!r}")
