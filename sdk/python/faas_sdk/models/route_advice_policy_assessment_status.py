from typing import Literal

RouteAdvicePolicyAssessmentStatus = Literal[
    "conflict", "duplicate", "no_matching_rule", "review_required", "unavailable"
]

ROUTE_ADVICE_POLICY_ASSESSMENT_STATUS_VALUES: set[RouteAdvicePolicyAssessmentStatus] = {
    "conflict",
    "duplicate",
    "no_matching_rule",
    "review_required",
    "unavailable",
}


def check_route_advice_policy_assessment_status(value: str) -> RouteAdvicePolicyAssessmentStatus:
    if value in ROUTE_ADVICE_POLICY_ASSESSMENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_ADVICE_POLICY_ASSESSMENT_STATUS_VALUES!r}")
