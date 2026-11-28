from typing import Literal

HostedTaskSourceIntentReviewStatus = Literal["approved", "expired", "pending", "rejected", "revoked", "stale"]

HOSTED_TASK_SOURCE_INTENT_REVIEW_STATUS_VALUES: set[HostedTaskSourceIntentReviewStatus] = {
    "approved",
    "expired",
    "pending",
    "rejected",
    "revoked",
    "stale",
}


def check_hosted_task_source_intent_review_status(value: str) -> HostedTaskSourceIntentReviewStatus:
    if value in HOSTED_TASK_SOURCE_INTENT_REVIEW_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_SOURCE_INTENT_REVIEW_STATUS_VALUES!r}")
