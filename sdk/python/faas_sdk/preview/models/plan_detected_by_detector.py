from typing import Literal

PlanDetectedByDetector = Literal["app_yaml", "compose", "fly", "k8s", "other", "procfile", "render", "serverless"]

PLAN_DETECTED_BY_DETECTOR_VALUES: set[PlanDetectedByDetector] = {
    "app_yaml",
    "compose",
    "fly",
    "k8s",
    "other",
    "procfile",
    "render",
    "serverless",
}


def check_plan_detected_by_detector(value: str) -> PlanDetectedByDetector:
    if value in PLAN_DETECTED_BY_DETECTOR_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_DETECTED_BY_DETECTOR_VALUES!r}")
