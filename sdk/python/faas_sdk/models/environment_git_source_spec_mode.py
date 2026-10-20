from typing import Literal

EnvironmentGitSourceSpecMode = Literal["enforce", "report"]

ENVIRONMENT_GIT_SOURCE_SPEC_MODE_VALUES: set[EnvironmentGitSourceSpecMode] = {
    "enforce",
    "report",
}


def check_environment_git_source_spec_mode(value: str) -> EnvironmentGitSourceSpecMode:
    if value in ENVIRONMENT_GIT_SOURCE_SPEC_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_SOURCE_SPEC_MODE_VALUES!r}")
