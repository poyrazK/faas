from typing import Literal

EventRecoveryPreflightReviewMode = Literal["workflow_retry"]

EVENT_RECOVERY_PREFLIGHT_REVIEW_MODE_VALUES: set[EventRecoveryPreflightReviewMode] = {
    "workflow_retry",
}


def check_event_recovery_preflight_review_mode(value: str) -> EventRecoveryPreflightReviewMode:
    if value in EVENT_RECOVERY_PREFLIGHT_REVIEW_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_PREFLIGHT_REVIEW_MODE_VALUES!r}")
