from typing import Literal

ObsRuntimePostCaptureItemJob = Literal["imaged"]

OBS_RUNTIME_POST_CAPTURE_ITEM_JOB_VALUES: set[ObsRuntimePostCaptureItemJob] = {
    "imaged",
}


def check_obs_runtime_post_capture_item_job(value: str) -> ObsRuntimePostCaptureItemJob:
    if value in OBS_RUNTIME_POST_CAPTURE_ITEM_JOB_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_POST_CAPTURE_ITEM_JOB_VALUES!r}")
