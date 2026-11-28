from typing import Literal

PlanDetectionWarningOutcome = Literal["merged", "skipped"]

PLAN_DETECTION_WARNING_OUTCOME_VALUES: set[PlanDetectionWarningOutcome] = {
    "merged",
    "skipped",
}


def check_plan_detection_warning_outcome(value: str) -> PlanDetectionWarningOutcome:
    if value in PLAN_DETECTION_WARNING_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_DETECTION_WARNING_OUTCOME_VALUES!r}")
