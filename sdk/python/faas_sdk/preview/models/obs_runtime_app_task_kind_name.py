from typing import Literal

ObsRuntimeAppTaskKindName = Literal["cron", "manual", "release"]

OBS_RUNTIME_APP_TASK_KIND_NAME_VALUES: set[ObsRuntimeAppTaskKindName] = {
    "cron",
    "manual",
    "release",
}


def check_obs_runtime_app_task_kind_name(value: str) -> ObsRuntimeAppTaskKindName:
    if value in OBS_RUNTIME_APP_TASK_KIND_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_APP_TASK_KIND_NAME_VALUES!r}")
