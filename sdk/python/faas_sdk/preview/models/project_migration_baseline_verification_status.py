from typing import Literal

ProjectMigrationBaselineVerificationStatus = Literal["failed", "healthy", "unknown"]

PROJECT_MIGRATION_BASELINE_VERIFICATION_STATUS_VALUES: set[ProjectMigrationBaselineVerificationStatus] = {
    "failed",
    "healthy",
    "unknown",
}


def check_project_migration_baseline_verification_status(value: str) -> ProjectMigrationBaselineVerificationStatus:
    if value in PROJECT_MIGRATION_BASELINE_VERIFICATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_MIGRATION_BASELINE_VERIFICATION_STATUS_VALUES!r}"
    )
