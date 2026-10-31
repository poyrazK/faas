from typing import Literal

ManagedPostgresDatabaseState = Literal["deleted", "deleting", "failed", "provisioning", "ready", "updating"]

MANAGED_POSTGRES_DATABASE_STATE_VALUES: set[ManagedPostgresDatabaseState] = {
    "deleted",
    "deleting",
    "failed",
    "provisioning",
    "ready",
    "updating",
}


def check_managed_postgres_database_state(value: str) -> ManagedPostgresDatabaseState:
    if value in MANAGED_POSTGRES_DATABASE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_DATABASE_STATE_VALUES!r}")
