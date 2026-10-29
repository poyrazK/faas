from typing import Literal

MigrationVerificationRecoveryStatus = Literal["blocked", "pending", "restored", "superseded"]

MIGRATION_VERIFICATION_RECOVERY_STATUS_VALUES: set[MigrationVerificationRecoveryStatus] = {
    "blocked",
    "pending",
    "restored",
    "superseded",
}


def check_migration_verification_recovery_status(value: str) -> MigrationVerificationRecoveryStatus:
    if value in MIGRATION_VERIFICATION_RECOVERY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIGRATION_VERIFICATION_RECOVERY_STATUS_VALUES!r}")
