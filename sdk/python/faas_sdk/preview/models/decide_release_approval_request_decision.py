from typing import Literal

DecideReleaseApprovalRequestDecision = Literal["approve", "reject"]

DECIDE_RELEASE_APPROVAL_REQUEST_DECISION_VALUES: set[DecideReleaseApprovalRequestDecision] = {
    "approve",
    "reject",
}


def check_decide_release_approval_request_decision(value: str) -> DecideReleaseApprovalRequestDecision:
    if value in DECIDE_RELEASE_APPROVAL_REQUEST_DECISION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DECIDE_RELEASE_APPROVAL_REQUEST_DECISION_VALUES!r}")
