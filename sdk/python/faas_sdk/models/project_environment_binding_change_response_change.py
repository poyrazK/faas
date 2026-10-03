from typing import Literal

ProjectEnvironmentBindingChangeResponseChange = Literal["added", "changed", "removed"]

PROJECT_ENVIRONMENT_BINDING_CHANGE_RESPONSE_CHANGE_VALUES: set[ProjectEnvironmentBindingChangeResponseChange] = {
    "added",
    "changed",
    "removed",
}


def check_project_environment_binding_change_response_change(
    value: str,
) -> ProjectEnvironmentBindingChangeResponseChange:
    if value in PROJECT_ENVIRONMENT_BINDING_CHANGE_RESPONSE_CHANGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_BINDING_CHANGE_RESPONSE_CHANGE_VALUES!r}"
    )
