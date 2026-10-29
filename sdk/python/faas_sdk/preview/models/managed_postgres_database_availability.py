from typing import Literal

ManagedPostgresDatabaseAvailability = Literal["high_availability", "single_zone"]

MANAGED_POSTGRES_DATABASE_AVAILABILITY_VALUES: set[ManagedPostgresDatabaseAvailability] = {
    "high_availability",
    "single_zone",
}


def check_managed_postgres_database_availability(value: str) -> ManagedPostgresDatabaseAvailability:
    if value in MANAGED_POSTGRES_DATABASE_AVAILABILITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_DATABASE_AVAILABILITY_VALUES!r}")
