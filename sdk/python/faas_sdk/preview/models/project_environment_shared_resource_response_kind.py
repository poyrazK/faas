from typing import Literal

ProjectEnvironmentSharedResourceResponseKind = Literal["domains", "policies", "routes"]

PROJECT_ENVIRONMENT_SHARED_RESOURCE_RESPONSE_KIND_VALUES: set[ProjectEnvironmentSharedResourceResponseKind] = {
    "domains",
    "policies",
    "routes",
}


def check_project_environment_shared_resource_response_kind(value: str) -> ProjectEnvironmentSharedResourceResponseKind:
    if value in PROJECT_ENVIRONMENT_SHARED_RESOURCE_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_SHARED_RESOURCE_RESPONSE_KIND_VALUES!r}"
    )
