from typing import Literal

ProjectEnvironmentSecretChangeResponseKind = Literal["added", "changed", "removed", "unknown", "version_drift"]

PROJECT_ENVIRONMENT_SECRET_CHANGE_RESPONSE_KIND_VALUES: set[ProjectEnvironmentSecretChangeResponseKind] = {
    "added",
    "changed",
    "removed",
    "unknown",
    "version_drift",
}


def check_project_environment_secret_change_response_kind(value: str) -> ProjectEnvironmentSecretChangeResponseKind:
    if value in PROJECT_ENVIRONMENT_SECRET_CHANGE_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_SECRET_CHANGE_RESPONSE_KIND_VALUES!r}"
    )
