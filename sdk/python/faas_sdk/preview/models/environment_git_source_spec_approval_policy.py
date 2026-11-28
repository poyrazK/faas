from typing import Literal

EnvironmentGitSourceSpecApprovalPolicy = Literal["manual", "protected_branch"]

ENVIRONMENT_GIT_SOURCE_SPEC_APPROVAL_POLICY_VALUES: set[EnvironmentGitSourceSpecApprovalPolicy] = {
    "manual",
    "protected_branch",
}


def check_environment_git_source_spec_approval_policy(value: str) -> EnvironmentGitSourceSpecApprovalPolicy:
    if value in ENVIRONMENT_GIT_SOURCE_SPEC_APPROVAL_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_SOURCE_SPEC_APPROVAL_POLICY_VALUES!r}"
    )
