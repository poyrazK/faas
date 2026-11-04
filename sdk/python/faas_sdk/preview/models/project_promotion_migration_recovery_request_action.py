from typing import Literal

ProjectPromotionMigrationRecoveryRequestAction = Literal["resolve", "resume"]

PROJECT_PROMOTION_MIGRATION_RECOVERY_REQUEST_ACTION_VALUES: set[ProjectPromotionMigrationRecoveryRequestAction] = {
    "resolve",
    "resume",
}


def check_project_promotion_migration_recovery_request_action(
    value: str,
) -> ProjectPromotionMigrationRecoveryRequestAction:
    if value in PROJECT_PROMOTION_MIGRATION_RECOVERY_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_PROMOTION_MIGRATION_RECOVERY_REQUEST_ACTION_VALUES!r}"
    )
