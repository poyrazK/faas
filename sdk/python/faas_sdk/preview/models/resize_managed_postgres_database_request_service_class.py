from typing import Literal

ResizeManagedPostgresDatabaseRequestServiceClass = Literal["burstable", "development", "production"]

RESIZE_MANAGED_POSTGRES_DATABASE_REQUEST_SERVICE_CLASS_VALUES: set[ResizeManagedPostgresDatabaseRequestServiceClass] = {
    "burstable",
    "development",
    "production",
}


def check_resize_managed_postgres_database_request_service_class(
    value: str,
) -> ResizeManagedPostgresDatabaseRequestServiceClass:
    if value in RESIZE_MANAGED_POSTGRES_DATABASE_REQUEST_SERVICE_CLASS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {RESIZE_MANAGED_POSTGRES_DATABASE_REQUEST_SERVICE_CLASS_VALUES!r}"
    )
