from typing import Literal

ProjectReleaseCommandCompatibility = Literal["backward-compatible"]

PROJECT_RELEASE_COMMAND_COMPATIBILITY_VALUES: set[ProjectReleaseCommandCompatibility] = {
    "backward-compatible",
}


def check_project_release_command_compatibility(value: str) -> ProjectReleaseCommandCompatibility:
    if value in PROJECT_RELEASE_COMMAND_COMPATIBILITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_RELEASE_COMMAND_COMPATIBILITY_VALUES!r}")
