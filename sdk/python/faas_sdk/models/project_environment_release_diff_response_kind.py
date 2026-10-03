from typing import Literal

ProjectEnvironmentReleaseDiffResponseKind = Literal["added", "changed", "removed", "unchanged"]

PROJECT_ENVIRONMENT_RELEASE_DIFF_RESPONSE_KIND_VALUES: set[ProjectEnvironmentReleaseDiffResponseKind] = {
    "added",
    "changed",
    "removed",
    "unchanged",
}


def check_project_environment_release_diff_response_kind(value: str) -> ProjectEnvironmentReleaseDiffResponseKind:
    if value in PROJECT_ENVIRONMENT_RELEASE_DIFF_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_RELEASE_DIFF_RESPONSE_KIND_VALUES!r}"
    )
