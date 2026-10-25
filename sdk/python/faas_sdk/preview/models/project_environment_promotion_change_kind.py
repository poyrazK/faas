from typing import Literal

ProjectEnvironmentPromotionChangeKind = Literal["create", "source_missing", "unchanged", "update"]

PROJECT_ENVIRONMENT_PROMOTION_CHANGE_KIND_VALUES: set[ProjectEnvironmentPromotionChangeKind] = {
    "create",
    "source_missing",
    "unchanged",
    "update",
}


def check_project_environment_promotion_change_kind(value: str) -> ProjectEnvironmentPromotionChangeKind:
    if value in PROJECT_ENVIRONMENT_PROMOTION_CHANGE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_CHANGE_KIND_VALUES!r}")
