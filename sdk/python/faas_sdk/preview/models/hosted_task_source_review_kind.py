from typing import Literal

HostedTaskSourceReviewKind = Literal["hosted_task_source_review"]

HOSTED_TASK_SOURCE_REVIEW_KIND_VALUES: set[HostedTaskSourceReviewKind] = {
    "hosted_task_source_review",
}


def check_hosted_task_source_review_kind(value: str) -> HostedTaskSourceReviewKind:
    if value in HOSTED_TASK_SOURCE_REVIEW_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {HOSTED_TASK_SOURCE_REVIEW_KIND_VALUES!r}")
