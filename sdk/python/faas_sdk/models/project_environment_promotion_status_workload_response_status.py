from typing import Literal

ProjectEnvironmentPromotionStatusWorkloadResponseStatus = Literal["failed", "pending", "promoted", "unchanged"]

PROJECT_ENVIRONMENT_PROMOTION_STATUS_WORKLOAD_RESPONSE_STATUS_VALUES: set[
    ProjectEnvironmentPromotionStatusWorkloadResponseStatus
] = {
    "failed",
    "pending",
    "promoted",
    "unchanged",
}


def check_project_environment_promotion_status_workload_response_status(
    value: str,
) -> ProjectEnvironmentPromotionStatusWorkloadResponseStatus:
    if value in PROJECT_ENVIRONMENT_PROMOTION_STATUS_WORKLOAD_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_STATUS_WORKLOAD_RESPONSE_STATUS_VALUES!r}"
    )
