from typing import Literal

TaskReleaseReviewVersion = Literal[1]

TASK_RELEASE_REVIEW_VERSION_VALUES: set[TaskReleaseReviewVersion] = {
    1,
}


def check_task_release_review_version(value: int) -> TaskReleaseReviewVersion:
    if value in TASK_RELEASE_REVIEW_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TASK_RELEASE_REVIEW_VERSION_VALUES!r}")
