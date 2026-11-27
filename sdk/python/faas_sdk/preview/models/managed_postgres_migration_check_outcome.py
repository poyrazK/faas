from typing import Literal

ManagedPostgresMigrationCheckOutcome = Literal["blocked", "compatible", "unverified"]

MANAGED_POSTGRES_MIGRATION_CHECK_OUTCOME_VALUES: set[ManagedPostgresMigrationCheckOutcome] = {
    "blocked",
    "compatible",
    "unverified",
}


def check_managed_postgres_migration_check_outcome(value: str) -> ManagedPostgresMigrationCheckOutcome:
    if value in MANAGED_POSTGRES_MIGRATION_CHECK_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_MIGRATION_CHECK_OUTCOME_VALUES!r}")
