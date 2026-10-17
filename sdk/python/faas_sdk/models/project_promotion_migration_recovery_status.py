from typing import Literal

ProjectPromotionMigrationRecoveryStatus = Literal["blocked", "quiescing", "resolved", "resumed", "verifying"]

PROJECT_PROMOTION_MIGRATION_RECOVERY_STATUS_VALUES: set[ProjectPromotionMigrationRecoveryStatus] = {
    "blocked",
    "quiescing",
    "resolved",
    "resumed",
    "verifying",
}


def check_project_promotion_migration_recovery_status(value: str) -> ProjectPromotionMigrationRecoveryStatus:
    if value in PROJECT_PROMOTION_MIGRATION_RECOVERY_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_PROMOTION_MIGRATION_RECOVERY_STATUS_VALUES!r}"
    )
