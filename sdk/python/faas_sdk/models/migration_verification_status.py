from typing import Literal

MigrationVerificationStatus = Literal["blocked", "cancelled", "expired", "failed", "passed", "pending"]

MIGRATION_VERIFICATION_STATUS_VALUES: set[MigrationVerificationStatus] = {
    "blocked",
    "cancelled",
    "expired",
    "failed",
    "passed",
    "pending",
}


def check_migration_verification_status(value: str) -> MigrationVerificationStatus:
    if value in MIGRATION_VERIFICATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIGRATION_VERIFICATION_STATUS_VALUES!r}")
