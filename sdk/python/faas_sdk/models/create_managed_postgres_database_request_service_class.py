from typing import Literal

CreateManagedPostgresDatabaseRequestServiceClass = Literal["burstable", "development", "production"]

CREATE_MANAGED_POSTGRES_DATABASE_REQUEST_SERVICE_CLASS_VALUES: set[CreateManagedPostgresDatabaseRequestServiceClass] = {
    "burstable",
    "development",
    "production",
}


def check_create_managed_postgres_database_request_service_class(
    value: str,
) -> CreateManagedPostgresDatabaseRequestServiceClass:
    if value in CREATE_MANAGED_POSTGRES_DATABASE_REQUEST_SERVICE_CLASS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_MANAGED_POSTGRES_DATABASE_REQUEST_SERVICE_CLASS_VALUES!r}"
    )
