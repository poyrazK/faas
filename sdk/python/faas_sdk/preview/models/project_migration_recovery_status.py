from typing import Literal

ProjectMigrationRecoveryStatus = Literal["blocked", "needs_operator", "not_required", "resolved"]

PROJECT_MIGRATION_RECOVERY_STATUS_VALUES: set[ProjectMigrationRecoveryStatus] = {
    "blocked",
    "needs_operator",
    "not_required",
    "resolved",
}


def check_project_migration_recovery_status(value: str) -> ProjectMigrationRecoveryStatus:
    if value in PROJECT_MIGRATION_RECOVERY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_MIGRATION_RECOVERY_STATUS_VALUES!r}")
