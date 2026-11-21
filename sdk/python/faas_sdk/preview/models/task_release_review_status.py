from typing import Literal

TaskReleaseReviewStatus = Literal["review_only"]

TASK_RELEASE_REVIEW_STATUS_VALUES: set[TaskReleaseReviewStatus] = {
    "review_only",
}


def check_task_release_review_status(value: str) -> TaskReleaseReviewStatus:
    if value in TASK_RELEASE_REVIEW_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_RELEASE_REVIEW_STATUS_VALUES!r}")
