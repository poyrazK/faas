from typing import Literal

ReleaseApprovalPolicyApprovers = Literal["admin", "developer"]

RELEASE_APPROVAL_POLICY_APPROVERS_VALUES: set[ReleaseApprovalPolicyApprovers] = {
    "admin",
    "developer",
}


def check_release_approval_policy_approvers(value: str) -> ReleaseApprovalPolicyApprovers:
    if value in RELEASE_APPROVAL_POLICY_APPROVERS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_APPROVAL_POLICY_APPROVERS_VALUES!r}")
