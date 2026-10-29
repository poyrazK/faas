from typing import Literal

ManagedPostgresDatabaseServiceClass = Literal["burstable", "development", "production"]

MANAGED_POSTGRES_DATABASE_SERVICE_CLASS_VALUES: set[ManagedPostgresDatabaseServiceClass] = {
    "burstable",
    "development",
    "production",
}


def check_managed_postgres_database_service_class(value: str) -> ManagedPostgresDatabaseServiceClass:
    if value in MANAGED_POSTGRES_DATABASE_SERVICE_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_DATABASE_SERVICE_CLASS_VALUES!r}")
