from typing import Literal

CreateEnvironmentGitSourceRequestApprovalPolicy = Literal["manual"]

CREATE_ENVIRONMENT_GIT_SOURCE_REQUEST_APPROVAL_POLICY_VALUES: set[CreateEnvironmentGitSourceRequestApprovalPolicy] = {
    "manual",
}


def check_create_environment_git_source_request_approval_policy(
    value: str,
) -> CreateEnvironmentGitSourceRequestApprovalPolicy:
    if value in CREATE_ENVIRONMENT_GIT_SOURCE_REQUEST_APPROVAL_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_ENVIRONMENT_GIT_SOURCE_REQUEST_APPROVAL_POLICY_VALUES!r}"
    )
