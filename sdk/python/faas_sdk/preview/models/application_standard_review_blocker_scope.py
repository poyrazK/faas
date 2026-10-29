from typing import Literal

ApplicationStandardReviewBlockerScope = Literal["application", "organization", "project"]

APPLICATION_STANDARD_REVIEW_BLOCKER_SCOPE_VALUES: set[ApplicationStandardReviewBlockerScope] = {
    "application",
    "organization",
    "project",
}


def check_application_standard_review_blocker_scope(value: str) -> ApplicationStandardReviewBlockerScope:
    if value in APPLICATION_STANDARD_REVIEW_BLOCKER_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_REVIEW_BLOCKER_SCOPE_VALUES!r}")
