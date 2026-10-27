from typing import Literal

ProjectEnvironmentSharedResourceResponseOwnership = Literal["application"]

PROJECT_ENVIRONMENT_SHARED_RESOURCE_RESPONSE_OWNERSHIP_VALUES: set[
    ProjectEnvironmentSharedResourceResponseOwnership
] = {
    "application",
}


def check_project_environment_shared_resource_response_ownership(
    value: str,
) -> ProjectEnvironmentSharedResourceResponseOwnership:
    if value in PROJECT_ENVIRONMENT_SHARED_RESOURCE_RESPONSE_OWNERSHIP_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_SHARED_RESOURCE_RESPONSE_OWNERSHIP_VALUES!r}"
    )
