from typing import Literal

ProjectEnvironmentVariableChangeResponseKind = Literal["added", "changed", "removed"]

PROJECT_ENVIRONMENT_VARIABLE_CHANGE_RESPONSE_KIND_VALUES: set[ProjectEnvironmentVariableChangeResponseKind] = {
    "added",
    "changed",
    "removed",
}


def check_project_environment_variable_change_response_kind(value: str) -> ProjectEnvironmentVariableChangeResponseKind:
    if value in PROJECT_ENVIRONMENT_VARIABLE_CHANGE_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_VARIABLE_CHANGE_RESPONSE_KIND_VALUES!r}"
    )
