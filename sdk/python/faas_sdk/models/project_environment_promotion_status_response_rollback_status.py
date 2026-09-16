from typing import Literal

ProjectEnvironmentPromotionStatusResponseRollbackStatus = Literal["rollback_failed", "rolled_back", "rolling_back"]

PROJECT_ENVIRONMENT_PROMOTION_STATUS_RESPONSE_ROLLBACK_STATUS_VALUES: set[
    ProjectEnvironmentPromotionStatusResponseRollbackStatus
] = {
    "rollback_failed",
    "rolled_back",
    "rolling_back",
}


def check_project_environment_promotion_status_response_rollback_status(
    value: str,
) -> ProjectEnvironmentPromotionStatusResponseRollbackStatus:
    if value in PROJECT_ENVIRONMENT_PROMOTION_STATUS_RESPONSE_ROLLBACK_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_STATUS_RESPONSE_ROLLBACK_STATUS_VALUES!r}"
    )
