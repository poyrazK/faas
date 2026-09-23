from typing import Literal

ProjectEnvironmentBindingResponseKind = Literal["managed_postgres", "object_storage"]

PROJECT_ENVIRONMENT_BINDING_RESPONSE_KIND_VALUES: set[ProjectEnvironmentBindingResponseKind] = {
    "managed_postgres",
    "object_storage",
}


def check_project_environment_binding_response_kind(value: str) -> ProjectEnvironmentBindingResponseKind:
    if value in PROJECT_ENVIRONMENT_BINDING_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_BINDING_RESPONSE_KIND_VALUES!r}")
