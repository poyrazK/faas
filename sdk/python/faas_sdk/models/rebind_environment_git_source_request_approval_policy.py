from typing import Literal

RebindEnvironmentGitSourceRequestApprovalPolicy = Literal["manual", "protected_branch"]

REBIND_ENVIRONMENT_GIT_SOURCE_REQUEST_APPROVAL_POLICY_VALUES: set[RebindEnvironmentGitSourceRequestApprovalPolicy] = {
    "manual",
    "protected_branch",
}


def check_rebind_environment_git_source_request_approval_policy(
    value: str,
) -> RebindEnvironmentGitSourceRequestApprovalPolicy:
    if value in REBIND_ENVIRONMENT_GIT_SOURCE_REQUEST_APPROVAL_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REBIND_ENVIRONMENT_GIT_SOURCE_REQUEST_APPROVAL_POLICY_VALUES!r}"
    )
