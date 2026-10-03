from typing import Literal

ProjectEnvironmentSecretResponseManagedBy = Literal["managed_postgres", "object_storage"]

PROJECT_ENVIRONMENT_SECRET_RESPONSE_MANAGED_BY_VALUES: set[ProjectEnvironmentSecretResponseManagedBy] = {
    "managed_postgres",
    "object_storage",
}


def check_project_environment_secret_response_managed_by(value: str) -> ProjectEnvironmentSecretResponseManagedBy:
    if value in PROJECT_ENVIRONMENT_SECRET_RESPONSE_MANAGED_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_SECRET_RESPONSE_MANAGED_BY_VALUES!r}"
    )
