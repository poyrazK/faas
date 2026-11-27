from typing import Literal

ManagedPostgresRecoveryStatusOutcome = Literal["covered", "no_base_backup", "unverified", "wal_gap"]

MANAGED_POSTGRES_RECOVERY_STATUS_OUTCOME_VALUES: set[ManagedPostgresRecoveryStatusOutcome] = {
    "covered",
    "no_base_backup",
    "unverified",
    "wal_gap",
}


def check_managed_postgres_recovery_status_outcome(value: str) -> ManagedPostgresRecoveryStatusOutcome:
    if value in MANAGED_POSTGRES_RECOVERY_STATUS_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_RECOVERY_STATUS_OUTCOME_VALUES!r}")
