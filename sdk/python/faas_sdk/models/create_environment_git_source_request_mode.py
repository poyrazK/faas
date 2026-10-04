from typing import Literal

CreateEnvironmentGitSourceRequestMode = Literal["enforce", "report"]

CREATE_ENVIRONMENT_GIT_SOURCE_REQUEST_MODE_VALUES: set[CreateEnvironmentGitSourceRequestMode] = {
    "enforce",
    "report",
}


def check_create_environment_git_source_request_mode(value: str) -> CreateEnvironmentGitSourceRequestMode:
    if value in CREATE_ENVIRONMENT_GIT_SOURCE_REQUEST_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_ENVIRONMENT_GIT_SOURCE_REQUEST_MODE_VALUES!r}"
    )
