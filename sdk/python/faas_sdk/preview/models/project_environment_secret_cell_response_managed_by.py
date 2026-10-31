from typing import Literal

ProjectEnvironmentSecretCellResponseManagedBy = Literal["managed_postgres", "object_storage"]

PROJECT_ENVIRONMENT_SECRET_CELL_RESPONSE_MANAGED_BY_VALUES: set[ProjectEnvironmentSecretCellResponseManagedBy] = {
    "managed_postgres",
    "object_storage",
}


def check_project_environment_secret_cell_response_managed_by(
    value: str,
) -> ProjectEnvironmentSecretCellResponseManagedBy:
    if value in PROJECT_ENVIRONMENT_SECRET_CELL_RESPONSE_MANAGED_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_SECRET_CELL_RESPONSE_MANAGED_BY_VALUES!r}"
    )
