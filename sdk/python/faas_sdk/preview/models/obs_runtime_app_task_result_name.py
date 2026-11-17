from typing import Literal

ObsRuntimeAppTaskResultName = Literal[
    "cancelled", "failed", "failed_runs", "queued", "requeued_restores", "succeeded", "timed_out"
]

OBS_RUNTIME_APP_TASK_RESULT_NAME_VALUES: set[ObsRuntimeAppTaskResultName] = {
    "cancelled",
    "failed",
    "failed_runs",
    "queued",
    "requeued_restores",
    "succeeded",
    "timed_out",
}


def check_obs_runtime_app_task_result_name(value: str) -> ObsRuntimeAppTaskResultName:
    if value in OBS_RUNTIME_APP_TASK_RESULT_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_RESULT_NAME_VALUES!r}")
