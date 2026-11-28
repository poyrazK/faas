from typing import Literal

ObsRuntimeAppTaskResultsScope = Literal["process_local_counter_observations"]

OBS_RUNTIME_APP_TASK_RESULTS_SCOPE_VALUES: set[ObsRuntimeAppTaskResultsScope] = {
    "process_local_counter_observations",
}


def check_obs_runtime_app_task_results_scope(value: str) -> ObsRuntimeAppTaskResultsScope:
    if value in OBS_RUNTIME_APP_TASK_RESULTS_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_RESULTS_SCOPE_VALUES!r}")
