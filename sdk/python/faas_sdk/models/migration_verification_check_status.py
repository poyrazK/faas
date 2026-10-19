from typing import Literal

MigrationVerificationCheckStatus = Literal["failed", "passed", "pending"]

MIGRATION_VERIFICATION_CHECK_STATUS_VALUES: set[MigrationVerificationCheckStatus] = {
    "failed",
    "passed",
    "pending",
}


def check_migration_verification_check_status(value: str) -> MigrationVerificationCheckStatus:
    if value in MIGRATION_VERIFICATION_CHECK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIGRATION_VERIFICATION_CHECK_STATUS_VALUES!r}")
