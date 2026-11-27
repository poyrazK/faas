from typing import Literal

ManagedPostgresMigrationPreparationState = Literal["blocked", "paused", "prepared", "preparing"]

MANAGED_POSTGRES_MIGRATION_PREPARATION_STATE_VALUES: set[ManagedPostgresMigrationPreparationState] = {
    "blocked",
    "paused",
    "prepared",
    "preparing",
}


def check_managed_postgres_migration_preparation_state(value: str) -> ManagedPostgresMigrationPreparationState:
    if value in MANAGED_POSTGRES_MIGRATION_PREPARATION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_MIGRATION_PREPARATION_STATE_VALUES!r}"
    )
