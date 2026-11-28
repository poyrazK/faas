from typing import Literal

ObsRuntimePostCaptureClassName = Literal[
    "activation_pending",
    "compatibility_held",
    "dependency_held",
    "gate_overdue",
    "hosting_retry",
    "hosting_verifying",
    "publication_pending",
    "unknown",
]

OBS_RUNTIME_POST_CAPTURE_CLASS_NAME_VALUES: set[ObsRuntimePostCaptureClassName] = {
    "activation_pending",
    "compatibility_held",
    "dependency_held",
    "gate_overdue",
    "hosting_retry",
    "hosting_verifying",
    "publication_pending",
    "unknown",
}


def check_obs_runtime_post_capture_class_name(value: str) -> ObsRuntimePostCaptureClassName:
    if value in OBS_RUNTIME_POST_CAPTURE_CLASS_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_POST_CAPTURE_CLASS_NAME_VALUES!r}")
