from typing import Literal

ProjectEnvironmentBindingChangeResponseKind = Literal["managed_postgres", "object_storage"]

PROJECT_ENVIRONMENT_BINDING_CHANGE_RESPONSE_KIND_VALUES: set[ProjectEnvironmentBindingChangeResponseKind] = {
    "managed_postgres",
    "object_storage",
}


def check_project_environment_binding_change_response_kind(value: str) -> ProjectEnvironmentBindingChangeResponseKind:
    if value in PROJECT_ENVIRONMENT_BINDING_CHANGE_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_BINDING_CHANGE_RESPONSE_KIND_VALUES!r}"
    )
