from typing import Literal

EnvironmentGitSourceSpecProvider = Literal["github", "gitlab"]

ENVIRONMENT_GIT_SOURCE_SPEC_PROVIDER_VALUES: set[EnvironmentGitSourceSpecProvider] = {
    "github",
    "gitlab",
}


def check_environment_git_source_spec_provider(value: str) -> EnvironmentGitSourceSpecProvider:
    if value in ENVIRONMENT_GIT_SOURCE_SPEC_PROVIDER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_SOURCE_SPEC_PROVIDER_VALUES!r}")
