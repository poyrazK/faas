from typing import Literal

ProjectEnvironmentPromotionStatusWorkloadResponseRollbackStatus = Literal[
    "cleared", "failed", "pending", "restored", "skipped", "unchanged"
]

PROJECT_ENVIRONMENT_PROMOTION_STATUS_WORKLOAD_RESPONSE_ROLLBACK_STATUS_VALUES: set[
    ProjectEnvironmentPromotionStatusWorkloadResponseRollbackStatus
] = {
    "cleared",
    "failed",
    "pending",
    "restored",
    "skipped",
    "unchanged",
}


def check_project_environment_promotion_status_workload_response_rollback_status(
    value: str,
) -> ProjectEnvironmentPromotionStatusWorkloadResponseRollbackStatus:
    if value in PROJECT_ENVIRONMENT_PROMOTION_STATUS_WORKLOAD_RESPONSE_ROLLBACK_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_STATUS_WORKLOAD_RESPONSE_ROLLBACK_STATUS_VALUES!r}"
    )
