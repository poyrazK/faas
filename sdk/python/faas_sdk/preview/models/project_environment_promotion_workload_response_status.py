from typing import Literal

ProjectEnvironmentPromotionWorkloadResponseStatus = Literal["promoted", "unchanged"]

PROJECT_ENVIRONMENT_PROMOTION_WORKLOAD_RESPONSE_STATUS_VALUES: set[
    ProjectEnvironmentPromotionWorkloadResponseStatus
] = {
    "promoted",
    "unchanged",
}


def check_project_environment_promotion_workload_response_status(
    value: str,
) -> ProjectEnvironmentPromotionWorkloadResponseStatus:
    if value in PROJECT_ENVIRONMENT_PROMOTION_WORKLOAD_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_PROMOTION_WORKLOAD_RESPONSE_STATUS_VALUES!r}"
    )
