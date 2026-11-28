from typing import Literal

ObsRuntimeAppTaskResultSource = Literal["completion", "recovery"]

OBS_RUNTIME_APP_TASK_RESULT_SOURCE_VALUES: set[ObsRuntimeAppTaskResultSource] = {
    "completion",
    "recovery",
}


def check_obs_runtime_app_task_result_source(value: str) -> ObsRuntimeAppTaskResultSource:
    if value in OBS_RUNTIME_APP_TASK_RESULT_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_RESULT_SOURCE_VALUES!r}")
