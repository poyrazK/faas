from typing import Literal

ApplicationStandardReviewBlockerField = Literal[
    "egress_cidrs", "egress_extra_ports", "log_destinations", "require_signed", "security_policy", "trusted_publishers"
]

APPLICATION_STANDARD_REVIEW_BLOCKER_FIELD_VALUES: set[ApplicationStandardReviewBlockerField] = {
    "egress_cidrs",
    "egress_extra_ports",
    "log_destinations",
    "require_signed",
    "security_policy",
    "trusted_publishers",
}


def check_application_standard_review_blocker_field(value: str) -> ApplicationStandardReviewBlockerField:
    if value in APPLICATION_STANDARD_REVIEW_BLOCKER_FIELD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APPLICATION_STANDARD_REVIEW_BLOCKER_FIELD_VALUES!r}")
