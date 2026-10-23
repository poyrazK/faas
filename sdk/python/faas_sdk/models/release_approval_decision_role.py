from typing import Literal

ReleaseApprovalDecisionRole = Literal["admin", "developer", "owner", "personal"]

RELEASE_APPROVAL_DECISION_ROLE_VALUES: set[ReleaseApprovalDecisionRole] = {
    "admin",
    "developer",
    "owner",
    "personal",
}


def check_release_approval_decision_role(value: str) -> ReleaseApprovalDecisionRole:
    if value in RELEASE_APPROVAL_DECISION_ROLE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_APPROVAL_DECISION_ROLE_VALUES!r}")
