from typing import Literal

SetReleaseApprovalPolicyRequestApprovers = Literal["admin", "developer"]

SET_RELEASE_APPROVAL_POLICY_REQUEST_APPROVERS_VALUES: set[SetReleaseApprovalPolicyRequestApprovers] = {
    "admin",
    "developer",
}


def check_set_release_approval_policy_request_approvers(value: str) -> SetReleaseApprovalPolicyRequestApprovers:
    if value in SET_RELEASE_APPROVAL_POLICY_REQUEST_APPROVERS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SET_RELEASE_APPROVAL_POLICY_REQUEST_APPROVERS_VALUES!r}"
    )
