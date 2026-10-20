from typing import Literal

AdoptEnvironmentGitOpsResponseStatus = Literal["adopted"]

ADOPT_ENVIRONMENT_GIT_OPS_RESPONSE_STATUS_VALUES: set[AdoptEnvironmentGitOpsResponseStatus] = {
    "adopted",
}


def check_adopt_environment_git_ops_response_status(value: str) -> AdoptEnvironmentGitOpsResponseStatus:
    if value in ADOPT_ENVIRONMENT_GIT_OPS_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ADOPT_ENVIRONMENT_GIT_OPS_RESPONSE_STATUS_VALUES!r}")
