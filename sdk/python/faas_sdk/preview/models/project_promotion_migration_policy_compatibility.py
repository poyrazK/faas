from typing import Literal

ProjectPromotionMigrationPolicyCompatibility = Literal["backward-compatible"]

PROJECT_PROMOTION_MIGRATION_POLICY_COMPATIBILITY_VALUES: set[ProjectPromotionMigrationPolicyCompatibility] = {
    "backward-compatible",
}


def check_project_promotion_migration_policy_compatibility(value: str) -> ProjectPromotionMigrationPolicyCompatibility:
    if value in PROJECT_PROMOTION_MIGRATION_POLICY_COMPATIBILITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_PROMOTION_MIGRATION_POLICY_COMPATIBILITY_VALUES!r}"
    )
