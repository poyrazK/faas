from typing import Literal

ObsRuntimeAppTaskClassName = Literal[
    "cancellation_pending",
    "deadline_overdue",
    "held",
    "lease_expired",
    "queued",
    "restoring",
    "retry_wait",
    "running",
    "unknown",
]

OBS_RUNTIME_APP_TASK_CLASS_NAME_VALUES: set[ObsRuntimeAppTaskClassName] = {
    "cancellation_pending",
    "deadline_overdue",
    "held",
    "lease_expired",
    "queued",
    "restoring",
    "retry_wait",
    "running",
    "unknown",
}


def check_obs_runtime_app_task_class_name(value: str) -> ObsRuntimeAppTaskClassName:
    if value in OBS_RUNTIME_APP_TASK_CLASS_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_CLASS_NAME_VALUES!r}")
