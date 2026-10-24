from typing import Literal

ProjectEnvironmentPromotionSummaryResponseRollbackStatus = Literal["rollback_failed", "rolled_back", "rolling_back"]

PROJECT_ENVIRONMENT_PROMOTION_SUMMARY_RESPONSE_ROLLBACK_STATUS_VALUES: set[
    ProjectEnvironmentPromotionSummaryResponseRollbackStatus
] = {
    "rollback_failed",
    "rolled_back",
    "rolling_back",
}


def check_project_environment_promotion_summary_response_rollback_status(
    value: str,
) -> ProjectEnvironmentPromotionSummaryResponseRollbackStatus:
    if value in PROJECT_ENVIRONMENT_PROMOTION_SUMMARY_RESPONSE_ROLLBACK_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_SUMMARY_RESPONSE_ROLLBACK_STATUS_VALUES!r}"
    )
