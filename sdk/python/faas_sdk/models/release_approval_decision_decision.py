from typing import Literal

ReleaseApprovalDecisionDecision = Literal["approve", "reject"]

RELEASE_APPROVAL_DECISION_DECISION_VALUES: set[ReleaseApprovalDecisionDecision] = {
    "approve",
    "reject",
}


def check_release_approval_decision_decision(value: str) -> ReleaseApprovalDecisionDecision:
    if value in RELEASE_APPROVAL_DECISION_DECISION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_APPROVAL_DECISION_DECISION_VALUES!r}")
