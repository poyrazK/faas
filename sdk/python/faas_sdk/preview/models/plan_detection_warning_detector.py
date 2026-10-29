from typing import Literal

PlanDetectionWarningDetector = Literal["app_yaml", "compose", "fly", "k8s", "other", "procfile", "render", "serverless"]

PLAN_DETECTION_WARNING_DETECTOR_VALUES: set[PlanDetectionWarningDetector] = {
    "app_yaml",
    "compose",
    "fly",
    "k8s",
    "other",
    "procfile",
    "render",
    "serverless",
}


def check_plan_detection_warning_detector(value: str) -> PlanDetectionWarningDetector:
    if value in PLAN_DETECTION_WARNING_DETECTOR_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_DETECTION_WARNING_DETECTOR_VALUES!r}")
