from typing import Literal

DebugRegressionReviewState = Literal["acknowledged", "active", "dismissed", "resolved"]

DEBUG_REGRESSION_REVIEW_STATE_VALUES: set[DebugRegressionReviewState] = {
    "acknowledged",
    "active",
    "dismissed",
    "resolved",
}


def check_debug_regression_review_state(value: str) -> DebugRegressionReviewState:
    if value in DEBUG_REGRESSION_REVIEW_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_REGRESSION_REVIEW_STATE_VALUES!r}")
