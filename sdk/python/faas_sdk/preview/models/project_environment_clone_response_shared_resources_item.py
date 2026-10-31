from typing import Literal

ProjectEnvironmentCloneResponseSharedResourcesItem = Literal[
    "domains", "managed_postgres_data", "object_storage_bucket_data", "policies", "routes"
]

PROJECT_ENVIRONMENT_CLONE_RESPONSE_SHARED_RESOURCES_ITEM_VALUES: set[
    ProjectEnvironmentCloneResponseSharedResourcesItem
] = {
    "domains",
    "managed_postgres_data",
    "object_storage_bucket_data",
    "policies",
    "routes",
}


def check_project_environment_clone_response_shared_resources_item(
    value: str,
) -> ProjectEnvironmentCloneResponseSharedResourcesItem:
    if value in PROJECT_ENVIRONMENT_CLONE_RESPONSE_SHARED_RESOURCES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_CLONE_RESPONSE_SHARED_RESOURCES_ITEM_VALUES!r}"
    )
