from typing import Literal

ManagedPostgresMigrationVerificationState = Literal["blocked", "paused", "verified", "verifying"]

MANAGED_POSTGRES_MIGRATION_VERIFICATION_STATE_VALUES: set[ManagedPostgresMigrationVerificationState] = {
    "blocked",
    "paused",
    "verified",
    "verifying",
}


def check_managed_postgres_migration_verification_state(value: str) -> ManagedPostgresMigrationVerificationState:
    if value in MANAGED_POSTGRES_MIGRATION_VERIFICATION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_MIGRATION_VERIFICATION_STATE_VALUES!r}"
    )
