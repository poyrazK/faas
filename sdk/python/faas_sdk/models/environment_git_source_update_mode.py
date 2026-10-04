from typing import Literal

EnvironmentGitSourceUpdateMode = Literal["enforce", "report"]

ENVIRONMENT_GIT_SOURCE_UPDATE_MODE_VALUES: set[EnvironmentGitSourceUpdateMode] = {
    "enforce",
    "report",
}


def check_environment_git_source_update_mode(value: str) -> EnvironmentGitSourceUpdateMode:
    if value in ENVIRONMENT_GIT_SOURCE_UPDATE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_SOURCE_UPDATE_MODE_VALUES!r}")
