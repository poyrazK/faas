from typing import Literal

ManagedPostgresRestoreCheckOutcome = Literal["covered", "no_base_backup", "unverified", "wal_gap"]

MANAGED_POSTGRES_RESTORE_CHECK_OUTCOME_VALUES: set[ManagedPostgresRestoreCheckOutcome] = {
    "covered",
    "no_base_backup",
    "unverified",
    "wal_gap",
}


def check_managed_postgres_restore_check_outcome(value: str) -> ManagedPostgresRestoreCheckOutcome:
    if value in MANAGED_POSTGRES_RESTORE_CHECK_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_RESTORE_CHECK_OUTCOME_VALUES!r}")
