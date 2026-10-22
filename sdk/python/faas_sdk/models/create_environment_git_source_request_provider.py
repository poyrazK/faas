from typing import Literal

CreateEnvironmentGitSourceRequestProvider = Literal["github", "gitlab"]

CREATE_ENVIRONMENT_GIT_SOURCE_REQUEST_PROVIDER_VALUES: set[CreateEnvironmentGitSourceRequestProvider] = {
    "github",
    "gitlab",
}


def check_create_environment_git_source_request_provider(value: str) -> CreateEnvironmentGitSourceRequestProvider:
    if value in CREATE_ENVIRONMENT_GIT_SOURCE_REQUEST_PROVIDER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_ENVIRONMENT_GIT_SOURCE_REQUEST_PROVIDER_VALUES!r}"
    )
