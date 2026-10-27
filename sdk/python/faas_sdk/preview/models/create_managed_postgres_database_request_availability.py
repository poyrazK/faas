from typing import Literal

CreateManagedPostgresDatabaseRequestAvailability = Literal["high_availability", "single_zone"]

CREATE_MANAGED_POSTGRES_DATABASE_REQUEST_AVAILABILITY_VALUES: set[CreateManagedPostgresDatabaseRequestAvailability] = {
    "high_availability",
    "single_zone",
}


def check_create_managed_postgres_database_request_availability(
    value: str,
) -> CreateManagedPostgresDatabaseRequestAvailability:
    if value in CREATE_MANAGED_POSTGRES_DATABASE_REQUEST_AVAILABILITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_MANAGED_POSTGRES_DATABASE_REQUEST_AVAILABILITY_VALUES!r}"
    )
