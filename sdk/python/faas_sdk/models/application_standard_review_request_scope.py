from typing import Literal

ApplicationStandardReviewRequestScope = Literal["application", "organization", "project"]

APPLICATION_STANDARD_REVIEW_REQUEST_SCOPE_VALUES: set[ApplicationStandardReviewRequestScope] = {
    "application",
    "organization",
    "project",
}


def check_application_standard_review_request_scope(value: str) -> ApplicationStandardReviewRequestScope:
    if value in APPLICATION_STANDARD_REVIEW_REQUEST_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_REVIEW_REQUEST_SCOPE_VALUES!r}")
