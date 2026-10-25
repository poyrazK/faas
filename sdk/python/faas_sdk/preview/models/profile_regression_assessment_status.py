from typing import Literal

ProfileRegressionAssessmentStatus = Literal["inconclusive", "no_regression_detected", "regressed"]

PROFILE_REGRESSION_ASSESSMENT_STATUS_VALUES: set[ProfileRegressionAssessmentStatus] = {
    "inconclusive",
    "no_regression_detected",
    "regressed",
}


def check_profile_regression_assessment_status(value: str) -> ProfileRegressionAssessmentStatus:
    if value in PROFILE_REGRESSION_ASSESSMENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_REGRESSION_ASSESSMENT_STATUS_VALUES!r}")
